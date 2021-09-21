package websocket

import (
	"compress/flate"
	"io"
	"sync"
)

const (
	minCompressionLevel     = -2
	maxCompressionLevel     = flate.BestCompression
	defaultCompressionLevel = 1

	flateReaderTail = "\x00\x00\xff\xff" + "\x01\x00\x00\xff\xff"
)

var (
	flateWriterPools [maxCompressionLevel - minCompressionLevel + 1]sync.Pool
	flateReaderPool  = sync.Pool{New: func() interface{} {
		return flate.NewReader(nil)
	}}
)

func isValidCompressionLevel(level int) bool {
	return minCompressionLevel <= level && level <= maxCompressionLevel
}

func decompressReader(r io.Reader) (io.ReadCloser, error) {
	fr, _ := flateReaderPool.Get().(io.ReadCloser)
	err := fr.(flate.Resetter).Reset(r, nil)
	return &flateReadWrapper{fr}, err
}

type flateReadWrapper struct {
	fr io.ReadCloser
}

func (r *flateReadWrapper) Read(p []byte) (int, error) {
	if r.fr == nil {
		return 0, io.ErrClosedPipe
	}
	n, err := r.fr.Read(p)
	if err == io.EOF {
		// Preemptively place the reader back in the pool. This helps with
		// scenarios where the application does not call NextReader() soon after
		// this final read.
		r.Close()
	}
	return n, err
}

func (r *flateReadWrapper) Close() error {
	if r.fr == nil {
		return io.ErrClosedPipe
	}
	err := r.fr.Close()
	flateReaderPool.Put(r.fr)
	r.fr = nil
	return err
}

func compressWriter(w io.WriteCloser, level int) io.WriteCloser {
	p := &flateWriterPools[level-minCompressionLevel]
	fw, _ := p.Get().(*flate.Writer)
	tw := &truncWriter{w: w}
	if fw == nil {
		fw, _ = flate.NewWriter(tw, level)
	} else {
		fw.Reset(tw)
	}
	return &flateWriteWrapper{fw: fw, p: p}
}

type truncWriter struct {
	w io.WriteCloser
	n int
	p [4]byte
}

func (w *truncWriter) Write(p []byte) (int, error) {
	n := 0

	if w.n < len(w.p) {
		n = copy(w.p[w.n:], p)
		p = p[n:]
		w.n += n
		if len(p) == 0 {
			return n, nil
		}
	}

	m := len(p)
	if m > len(w.p) {
		m = len(w.p)
	}

	if nn, err := w.w.Write(w.p[:m]); err != nil {
		return n + nn, err
	}

	copy(w.p[:], w.p[m:])
	copy(w.p[len(w.p)-m:], p[len(p)-m:])
	nn, err := w.w.Write(p[:len(p)-m])
	return n + nn, err
}

type flateWriteWrapper struct {
	fw *flate.Writer
	p  *sync.Pool
}

func (w *flateWriteWrapper) Write(p []byte) (int, error) {
	return w.fw.Write(p)
}

func (w *flateWriteWrapper) Close() error {
	err := w.fw.Flush()
	w.p.Put(w.fw)
	w.fw = nil
	return err
}
