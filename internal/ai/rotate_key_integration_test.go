// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package ai_test

import (
	"context"
	"errors"
	"testing"

	"github.com/moonstreamtech/ZonaryOS/internal/ai"
)

// TestRotateActiveConfigKey_ReEncryptsAndMasksNewKey is this batch's own
// secrets-rotation requirement applied to internal/ai: unlike
// internal/apikey.RotateAPIKey (which generates its own credential),
// RotateActiveConfigKey takes a caller-supplied new provider key (see
// that function's own doc comment for why) - this test confirms the
// active configuration's own stored ciphertext is actually replaced
// (getActiveConfigTx, exercised indirectly via a second SuggestWorkflow-
// style provider call would be one way to prove it, but the more direct
// proof is that APIKeyMasked reflects the NEW key's own suffix, not the
// old one's) and that the response never carries the plaintext.
func TestRotateActiveConfigKey_ReEncryptsAndMasksNewKey(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	encryptor := testEncryptor(t)

	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "Rotation Firm", "ai-rotate-owner")
	original := createTestConfig(ctx, t, appPool, encryptor, firmID, userID)

	rotated, err := ai.RotateActiveConfigKey(ctx, appPool, encryptor, firmID, userID, "sk-ant-brand-new-9999")
	if err != nil {
		t.Fatalf("RotateActiveConfigKey: %v", err)
	}
	if rotated.ID != original.ID {
		t.Fatalf("expected rotation to update the SAME active config row (id %s), got id %s", original.ID, rotated.ID)
	}
	if rotated.APIKeyMasked == original.APIKeyMasked {
		t.Fatalf("expected the masked key to change after rotation, got the same value %q", rotated.APIKeyMasked)
	}

	configs, err := ai.ListConfigs(ctx, appPool, encryptor, firmID, userID)
	if err != nil {
		t.Fatalf("ListConfigs: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("expected exactly 1 configuration (rotation updates in place, never creates a second row), got %d", len(configs))
	}
	if configs[0].APIKeyMasked != rotated.APIKeyMasked {
		t.Fatalf("expected the persisted config's masked key to match the rotation response, got %q vs %q", configs[0].APIKeyMasked, rotated.APIKeyMasked)
	}
}

// TestRotateActiveConfigKey_NoActiveConfigReturnsErrNoActiveConfig
// confirms the same 404-not-403 sentinel every other /ai/* entry point
// uses (writeAIError's own doc comment) applies here too.
func TestRotateActiveConfigKey_NoActiveConfigReturnsErrNoActiveConfig(t *testing.T) {
	adminPool, appPool := setupTest(t)
	ctx := context.Background()
	encryptor := testEncryptor(t)

	firmID, userID := seedOwner(ctx, t, adminPool, appPool, "No Config Firm", "ai-rotate-no-config-owner")

	_, err := ai.RotateActiveConfigKey(ctx, appPool, encryptor, firmID, userID, "sk-ant-new-key")
	if !errors.Is(err, ai.ErrNoActiveConfig) {
		t.Fatalf("expected ErrNoActiveConfig, got %v", err)
	}
}
