// Copyright (c) ZonaryOS. All rights reserved.
// Use of this source code is governed by the license found in the LICENSE
// file in the root of this repository (draft, pending legal review - see
// docs/OPEN_POINTS.md item 20).

package localization

import "errors"

var (
	// ErrFirmNotFound means the caller isn't a member of the given firm
	// at all - same convention as every other package's ErrFirmNotFound.
	ErrFirmNotFound = errors.New("firm not found")

	// ErrNotOwner means the caller is a real member of the firm but does
	// not hold an owner-flagged role - address/tax-rate writes are
	// owner-gated (see this package's doc comment).
	ErrNotOwner = errors.New("caller is not authorized to manage this firm's addresses or tax rates")

	// ErrAddressNotFound means no addresses row with the given id is
	// visible in the caller's firm context.
	ErrAddressNotFound = errors.New("address not found")

	// ErrInvalidAddress means CreateAddress/UpdateAddress was given a
	// structurally invalid address (missing line1/city/countryCode).
	ErrInvalidAddress = errors.New("invalid address")

	// ErrTaxRateNotFound means no tax_rates row with the given id is
	// visible in the caller's firm context.
	ErrTaxRateNotFound = errors.New("tax rate not found")

	// ErrInvalidTaxRate means CreateTaxRate/UpdateTaxRate was given a
	// structurally invalid tax rate (empty name, negative/malformed rate).
	ErrInvalidTaxRate = errors.New("invalid tax rate")

	// ErrFiscalCountryConfigNotFound means no fiscal_country_configs row
	// exists for the given country code - platform-wide reference data,
	// not firm-scoped, so this is never an ErrFirmNotFound-style
	// membership question.
	ErrFiscalCountryConfigNotFound = errors.New("fiscal country config not found")

	// ErrInvalidFiscalCountryConfig means Upsert/CreateFiscalCountryConfig
	// was given a structurally invalid config (empty required field, an
	// out-of-range fiscal year start month, or a VAT number pattern that
	// doesn't even compile as a regular expression).
	ErrInvalidFiscalCountryConfig = errors.New("invalid fiscal country config")
)
