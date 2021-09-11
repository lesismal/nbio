package nbio

import "github.com/lesismal/nbio/logging"

func SafeGo(fn func()) {
	go func() {
		defer func() {
			if err := recover(); err != nil {
				logging.Error("%v\n", err)
			}
		}()
		fn()
	}()

}
