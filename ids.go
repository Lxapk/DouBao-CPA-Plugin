package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// randomUUID returns a v4 UUID. The web client uses these for sequence ids,
// local message ids and the web_tab_id, and the gateway echoes them back, so
// they only need to be unique — not cryptographically special beyond what
// crypto/rand already gives.
func randomUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	dst := make([]byte, 36)
	hex.Encode(dst[0:8], b[0:4])
	dst[8] = '-'
	hex.Encode(dst[9:13], b[4:6])
	dst[13] = '-'
	hex.Encode(dst[14:18], b[6:8])
	dst[18] = '-'
	hex.Encode(dst[19:23], b[8:10])
	dst[23] = '-'
	hex.Encode(dst[24:36], b[10:16])
	return string(dst)
}

// randomNumericID returns a 19-digit id shaped like the device_id and web_id
// the browser generates.
//
// The gateway tolerates arbitrary values here, but a well-formed id keeps the
// request indistinguishable from a real client, which matters because these
// fields feed the anti-abuse binding.
func randomNumericID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%019d", time.Now().UnixNano())
	}
	n := uint64(0)
	for _, x := range b {
		n = n<<8 | uint64(x)
	}
	// Keep it 19 digits, the shape the web client uses.
	v := n % 9000000000000000000
	return fmt.Sprintf("%019d", v+1000000000000000000)
}

// nowMillis is the create_time unit the protocol expects (milliseconds).
func nowMillis() int64 { return time.Now().UnixMilli() }
