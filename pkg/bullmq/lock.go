package bullmq

import "github.com/google/uuid"

// LockToken represents a unique job ownership token
type LockToken string

// NewLockToken generates a new UUID v4 lock token (cryptographically random)
func NewLockToken() LockToken {
	return LockToken(uuid.New().String())
}

// String returns the lock token as a string
func (lt LockToken) String() string {
	return string(lt)
}
