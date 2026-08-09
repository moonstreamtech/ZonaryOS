// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package workflow

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// mockFieldValidator records how it was called - the confirmation this
// batch's validator-registry refactor asks for: a new FieldType can be
// added by registering one FieldValidator, and validatePayload's own
// dispatch loop (not a switch statement anymore) actually reaches it.
type mockFieldValidator struct {
	called   bool
	gotValue any
	gotSpec  FieldSpec
	err      error
}

func (m *mockFieldValidator) Validate(value any, spec FieldSpec, _ ValidationContext) error {
	m.called = true
	m.gotValue = value
	m.gotSpec = spec
	return m.err
}

// withMockFieldValidator registers mock under ft for the duration of the
// test, restoring (or removing) whatever was there before on cleanup -
// package-level fieldValidators is shared global state, so every test
// that touches it must clean up after itself to avoid bleeding into
// other tests.
func withMockFieldValidator(t *testing.T, ft FieldType, mock FieldValidator) {
	t.Helper()
	previous, hadPrevious := fieldValidators[ft]
	RegisterFieldValidator(ft, mock)
	t.Cleanup(func() {
		if hadPrevious {
			fieldValidators[ft] = previous
		} else {
			delete(fieldValidators, ft)
		}
	})
}

func TestValidatePayload_DispatchesToRegisteredMockValidator(t *testing.T) {
	const mockType FieldType = "mock_test_type"
	mock := &mockFieldValidator{}
	withMockFieldValidator(t, mockType, mock)

	fields := []FieldSpec{{Name: "custom", Type: mockType}}
	payload := map[string]any{"custom": "hello"}

	if err := validatePayload(context.Background(), nil, uuid.Nil, fields, payload); err != nil {
		t.Fatalf("validatePayload: %v", err)
	}
	if !mock.called {
		t.Fatal("expected validatePayload to dispatch to the registered mock validator")
	}
	if mock.gotValue != "hello" {
		t.Fatalf("expected the mock validator to receive value %q, got %v", "hello", mock.gotValue)
	}
	if mock.gotSpec.Name != "custom" {
		t.Fatalf("expected the mock validator to receive spec.Name %q, got %q", "custom", mock.gotSpec.Name)
	}
}

func TestValidatePayload_PropagatesMockValidatorError(t *testing.T) {
	const mockType FieldType = "mock_test_type_error"
	wantErr := errors.New("mock validator rejected this value")
	mock := &mockFieldValidator{err: wantErr}
	withMockFieldValidator(t, mockType, mock)

	fields := []FieldSpec{{Name: "custom", Type: mockType}}
	payload := map[string]any{"custom": "anything"}

	err := validatePayload(context.Background(), nil, uuid.Nil, fields, payload)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected validatePayload to propagate the mock validator's error, got %v", err)
	}
	if !mock.called {
		t.Fatal("expected the mock validator to have been called")
	}
}

// TestValidatePayload_MissingRequiredFieldNeverReachesValidator confirms
// the present/required/nil handling still happens once in validatePayload
// itself, before any FieldValidator is ever called - unchanged from
// before this batch's refactor (see validatePayload's own doc comment).
func TestValidatePayload_MissingRequiredFieldNeverReachesValidator(t *testing.T) {
	const mockType FieldType = "mock_test_type_required"
	mock := &mockFieldValidator{}
	withMockFieldValidator(t, mockType, mock)

	fields := []FieldSpec{{Name: "custom", Type: mockType, Required: true}}

	err := validatePayload(context.Background(), nil, uuid.Nil, fields, map[string]any{})
	if !errors.Is(err, ErrPayloadValidation) {
		t.Fatalf("expected ErrPayloadValidation for a missing required field, got %v", err)
	}
	if mock.called {
		t.Fatal("expected the validator to never be called for a missing required field")
	}
}
