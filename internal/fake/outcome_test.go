package fake_test

import (
	"errors"
	"testing"

	"github.com/Teagan42/forge/internal/fake"
)

func TestOutcomeQueue_ReturnsProgrammedResult(t *testing.T) {
	q := fake.NewOutcomeQueue[string]()
	q.ProgramResult("key-1", "hello")

	got, err := q.Next("key-1")
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if got != "hello" {
		t.Errorf("Next() = %q, want %q", got, "hello")
	}
}

func TestOutcomeQueue_ErrorsWhenUnprogrammedAndNoDefault(t *testing.T) {
	q := fake.NewOutcomeQueue[string]()
	if _, err := q.Next("unprogrammed"); err == nil {
		t.Fatal("Next() error = nil, want an error for an unprogrammed key")
	}
}

func TestOutcomeQueue_RepeatsFinalQueuedOutcomeOnLaterCalls(t *testing.T) {
	q := fake.NewOutcomeQueue[int]()
	q.ProgramResult("key", 1)
	q.ProgramResult("key", 2)

	first, err := q.Next("key")
	if err != nil {
		t.Fatalf("Next(first): %v", err)
	}
	second, err := q.Next("key")
	if err != nil {
		t.Fatalf("Next(second): %v", err)
	}
	third, err := q.Next("key")
	if err != nil {
		t.Fatalf("Next(third): %v", err)
	}

	if first != 1 {
		t.Errorf("first = %d, want 1", first)
	}
	if second != 2 || third != 2 {
		t.Errorf("second/third = %d/%d, want 2 repeated", second, third)
	}
}

func TestOutcomeQueue_ProgramErrorReturnsExactErrorWithoutFallingThroughToDefault(t *testing.T) {
	q := fake.NewOutcomeQueue[string]()
	sentinel := errors.New("boom")
	q.ProgramError("key-err", sentinel)
	q.ProgramDefault("should not be used")

	got, err := q.Next("key-err")
	if !errors.Is(err, sentinel) {
		t.Fatalf("Next() error = %v, want %v", err, sentinel)
	}
	if got != "" {
		t.Errorf("Next() value = %q, want zero value alongside a programmed error", got)
	}
}

func TestOutcomeQueue_ProgramDefaultAppliesOnlyToUnprogrammedKeys(t *testing.T) {
	q := fake.NewOutcomeQueue[string]()
	q.ProgramDefault("default value")
	q.ProgramResult("specific-key", "specific value")

	defaultResult, err := q.Next("unprogrammed-key")
	if err != nil {
		t.Fatalf("Next(unprogrammed): %v", err)
	}
	if defaultResult != "default value" {
		t.Errorf("unprogrammed key result = %q, want default %q", defaultResult, "default value")
	}

	specificResult, err := q.Next("specific-key")
	if err != nil {
		t.Fatalf("Next(specific-key): %v", err)
	}
	if specificResult != "specific value" {
		t.Errorf("specific-key result = %q, want its programmed value, not the default", specificResult)
	}
}

func TestOutcomeQueue_KeysAreConfiguredIndependently(t *testing.T) {
	q := fake.NewOutcomeQueue[string]()
	q.ProgramResult("key-a", "a")
	q.ProgramResult("key-b", "b")

	a, err := q.Next("key-a")
	if err != nil {
		t.Fatalf("Next(key-a): %v", err)
	}
	b, err := q.Next("key-b")
	if err != nil {
		t.Fatalf("Next(key-b): %v", err)
	}
	if a != "a" || b != "b" {
		t.Errorf("a/b = %q/%q, want a/b", a, b)
	}
}
