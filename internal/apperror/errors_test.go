package apperror

import (
	"errors"
	"testing"
)

func TestAppError(t *testing.T) {
	root := errors.New("root")
	tests := []struct {
		name    string
		err     *AppError
		wantMsg string
		wantCod int
	}{
		{"notfound", NewNotFound("missing"), "missing", 404},
		{"conflict", NewConflict("conflict"), "conflict", 409},
		{"bad", NewBadRequest("bad"), "bad", 400},
		{"forbidden", NewForbidden("forbidden"), "forbidden", 403},
		{"internal", NewInternal("internal", root), "internal: root", 500},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Code != tt.wantCod {
				t.Fatalf("Code=%d want=%d", tt.err.Code, tt.wantCod)
			}
			if tt.err.Error() != tt.wantMsg {
				t.Fatalf("Error()=%q want=%q", tt.err.Error(), tt.wantMsg)
			}
		})
	}

	internalErr := NewInternal("internal", root)
	if !errors.Is(internalErr.Unwrap(), root) {
		t.Fatalf("expected unwrap root error")
	}
}
