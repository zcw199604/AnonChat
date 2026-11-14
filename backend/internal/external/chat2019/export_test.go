package chat2019

import "time"

// SetSignAckTimeoutForTest overrides the sign ack wait used by Dial and returns a
// function restoring the previous value. It exists only so the external test
// package can keep tests fast.
func SetSignAckTimeoutForTest(d time.Duration) func() {
	previous := signAckTimeout
	signAckTimeout = d
	return func() { signAckTimeout = previous }
}
