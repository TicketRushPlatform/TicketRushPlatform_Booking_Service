package logger

import "testing"

func TestNewLogger(t *testing.T) {
	tests := []struct {
		name    string
		level   string
		wantErr bool
	}{
		{"ok", "info", false},
		{"bad", "invalid", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, err := NewLogger(Config{Level: tt.level})
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil || l == nil {
				t.Fatalf("unexpected err=%v logger=%v", err, l)
			}
		})
	}
}
