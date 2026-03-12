package types

const (
	AuthUnknown       = "unknown"
	AuthSeen          = "seen"
	AuthPending       = "auth_pending"
	AuthAuthenticated = "authenticated"
	AuthRejected      = "rejected"
	AuthExpired       = "expired"
)

const (
	TrustNone     = "none"
	TrustPending  = "pending"
	TrustTrusted  = "trusted"
	TrustRejected = "rejected"
	TrustRevoked  = "revoked"
)
