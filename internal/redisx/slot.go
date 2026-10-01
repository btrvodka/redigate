package redisx

import "strings"

// SlotCount is the number of hash slots in a redis cluster.
const SlotCount = 16384

// Slot returns the cluster hash slot of the key, honoring {hash tags}.
func Slot(key string) int {
	if start := strings.IndexByte(key, '{'); start >= 0 {
		if end := strings.IndexByte(key[start+1:], '}'); end > 0 {
			key = key[start+1 : start+1+end]
		}
	}

	return int(crc16(key) % SlotCount)
}

// crc16 implements CRC16-CCITT (XMODEM) used by redis cluster.
func crc16(s string) uint16 {
	var crc uint16

	for i := range len(s) {
		crc ^= uint16(s[i]) << 8 //nolint:mnd // CRC16 algorithm

		for range 8 {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}

	return crc
}
