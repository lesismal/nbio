package nbhttp

import (
	"crypto/tls"
	"net"
	"testing"

	ltls "github.com/lesismal/llib/std/crypto/tls"
	"github.com/lesismal/nbio"
)

func TestConn2String(t *testing.T) {
	var nbc = &nbio.Conn{ReadBuffer: []byte{1, 2, 3, 4}}
	snbc, err := conn2Array(nbc)
	if err != nil {
		t.Fatal(err)
	}
	nbc2, err := array2Conn(snbc)
	if err != nil {
		t.Fatal(err)
	}
	if nbc2 != nbc {
		t.Fatalf("nbc2 != nbc")
	}

	var tcp = &net.TCPConn{}
	stcp, err := conn2Array(tcp)
	if err != nil {
		t.Fatal(err)
	}
	tcp2, err := array2Conn(stcp)
	if err != nil {
		t.Fatal(err)
	}
	if tcp2 != tcp {
		t.Fatalf("tcp2 != tcp")
	}

	var unix = &net.UnixConn{}
	sunix, err := conn2Array(unix)
	if err != nil {
		t.Fatal(err)
	}
	unix2, err := array2Conn(sunix)
	if err != nil {
		t.Fatal(err)
	}
	if unix2 != unix {
		t.Fatalf("unix2 != unix")
	}

	var tls = &tls.Conn{}
	stls, err := conn2Array(tls)
	if err != nil {
		t.Fatal(err)
	}
	tls2, err := array2Conn(stls)
	if err != nil {
		t.Fatal(err)
	}
	if tls2 != tls {
		t.Fatalf("tls2 != tls")
	}

	var ltls = &ltls.Conn{}
	sltls, err := conn2Array(ltls)
	if err != nil {
		t.Fatal(err)
	}
	ltls2, err := array2Conn(sltls)
	if err != nil {
		t.Fatal(err)
	}
	if ltls2 != ltls {
		t.Fatalf("ltls2 != ltls")
	}

	var udp = &net.UDPConn{}
	_, err = conn2Array(udp)
	if err == nil {
		t.Fatal("err is nil")
	}
	_, err = array2Conn(connValue{'a', 'a', 'a'})
	if err == nil {
		t.Fatal("err is nil")
	}
	_, err = array2Conn(connValue{})
	if err == nil {
		t.Fatal("err is nil")
	}
}
