# Internal controls

The transaction flow leaves internal controls disabled unless the host supplies `WithInternalControls`. The host maps a transaction to an operator, action, amount and network, supplies an `Approver` callback and `AuditLog`, and configures a `Limit`. Limits are integer minor units scoped to network/action and UTC day; zero disables that bound. Approval or limit denial marks the existing transaction failed before prepare or broadcast. Audit append failures fail closed. Audit implementations must be append-only and retain rejected attempts; query by operator, time range or idempotency key.

The framework performs no custody or real funds operation. Tests use fake values and transactors. Limits and approvals are generic host policy primitives, not a compliance determination.

## Host compliance self-assessment (not legal advice)

For each deployment, the host should assess: (1) issuance and operation regions; (2) product form, including token or non-token; (3) fund-flow types, including issuance, payout, refund or settlement; and (4) applicable internal approval, per-operation and daily limits, audit retention and review access. This checklist is not legal advice; obtain qualified advice for the relevant deployment.
