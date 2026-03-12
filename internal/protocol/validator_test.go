package protocol

import (
	"testing"
	"time"
)

func TestValidateMessageSuccess(t *testing.T) {
	err := ValidateMessage("clawsynapse.auth.node-beta.challenge.request", ControlMessage{
		MessageType: "auth.challenge.request",
		To:          "node-beta",
		Ts:          time.Now().UnixMilli(),
	}, ValidateOptions{})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestValidateMessageModuleMismatch(t *testing.T) {
	err := ValidateMessage("clawsynapse.auth.node-beta.challenge.request", ControlMessage{
		MessageType: "trust.request",
		To:          "node-beta",
		Ts:          time.Now().UnixMilli(),
	}, ValidateOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
	if pe, ok := err.(*Error); !ok || pe.Code != ErrModuleMismatch {
		t.Fatalf("expected %s, got %v", ErrModuleMismatch, err)
	}
}

func TestValidateMessageTargetMismatch(t *testing.T) {
	err := ValidateMessage("clawsynapse.auth.node-beta.challenge.request", ControlMessage{
		MessageType: "auth.challenge.request",
		To:          "node-gamma",
		Ts:          time.Now().UnixMilli(),
	}, ValidateOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
	if pe, ok := err.(*Error); !ok || pe.Code != ErrTargetMismatch {
		t.Fatalf("expected %s, got %v", ErrTargetMismatch, err)
	}
}
