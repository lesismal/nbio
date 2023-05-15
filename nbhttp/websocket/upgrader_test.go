package websocket

import "testing"

func Test_validFrame(t *testing.T) {
	type args struct {
		opcode             MessageType
		fin                bool
		res1               bool
		res2               bool
		res3               bool
		expectingFragments bool
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{"validtext", args{TextMessage, true, false, false, false, false}, false},
		{"validbinary", args{BinaryMessage, true, false, false, false, false}, false},
		{"validbinaryFragmented", args{BinaryMessage, true, false, false, false, false}, false},
		{"reservedOpcode", args{MessageType(3), true, false, false, false, false}, true},
		{"reservedBit1", args{BinaryMessage, true, true, false, false, false}, true},
		{"reservedBit2", args{BinaryMessage, true, false, true, false, false}, true},
		{"reservedBit3", args{BinaryMessage, true, false, false, true, false}, true},
		{"CloseFragmented", args{CloseMessage, false, false, false, false, false}, true},
		{"ExpectingFragmentButGotText", args{TextMessage, false, false, false, false, true}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := NewUpgrader()
			wsc := NewConn(u, nil, "", true, true)
			if err := wsc.validFrame(tt.args.opcode, tt.args.fin, tt.args.res1, tt.args.res2, tt.args.res3, tt.args.expectingFragments); (err != nil) != tt.wantErr {
				t.Errorf("validFrame() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
