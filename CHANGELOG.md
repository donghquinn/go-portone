# Changelog

All notable changes to this project are documented here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [1.0.2] - Unreleased

### Changed

- Removed the `Version` constant. The User-Agent now carries the module version from the build (`go-portone/v1.0.2`), so it always matches the release tag.

## [1.0.1] - 2026-11-18

### Changed

- The module path is now `github.com/donghquinn/go-portone`, matching the repository. The `v1.0.0` tag declared `github.com/donghquinn/pg/portone` and cannot be installed from this repository.
- The package is generated from PortOne's OpenAPI document (1.16.0) and covers all 142 operations of the official server SDKs, grouped as `client.Payment`, `client.Payment.BillingKey`, `client.Platform.Partner`, and so on. `New` became `NewClient`, which returns an error for an empty secret or invalid base URL.
- API errors are `*APIError` values wrapping typed errors (`*PaymentNotFoundError`, `*PgProviderError`, …) for `errors.As`, replacing the `ErrType*` constants and `IsNotFound`.
- `WebhookVerifier.Verify` returns a `Webhook` interface (`*WebhookTransaction`, `*WebhookBillingKey`, or `*UnrecognizedVariant`). Event types are `WebhookType` constants such as `WebhookTypeTransactionPaid`.

### Added

- `VerifyWebhook` for verifying with a single secret.
- `Ptr` for optional request fields.
