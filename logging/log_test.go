package logging

import "testing"

func TestSetLogger(t *testing.T) {
	l := &logger{level: LevelDebug}
	SetLogger(l)
}

func TestSetLevel(t *testing.T) {
	SetLevel(LevelAll)
	func() {
		defer func() {
			err := recover()
			if err != nil {
				t.Errorf("recorver returned err: %s", err)
			}
		}()
		SetLevel(1000)
	}()
}

func Test_logger_SetLevel(t *testing.T) {
	l := &logger{level: LevelDebug}
	l.SetLevel(LevelAll)
}

func Test_logger_Debug(t *testing.T) {
	l := &logger{level: LevelDebug}
	l.Debug("logger debug test")
}

func Test_logger_Info(t *testing.T) {
	l := &logger{level: LevelDebug}
	l.Info("logger info test")
}

func Test_logger_Warn(t *testing.T) {
	l := &logger{level: LevelDebug}
	l.Warn("logger warn test")
}

func Test_logger_Error(t *testing.T) {
	l := &logger{level: LevelDebug}
	l.Error("logger error test")
}

func Test_Debug(t *testing.T) {
	Debug("log.Debug")
}

func Test_Info(t *testing.T) {
	Info("log.Info")
}

func Test_Warn(t *testing.T) {
	Warn("log.Warn")
}

func Test_Error(t *testing.T) {
	Error("log.Error")
}
