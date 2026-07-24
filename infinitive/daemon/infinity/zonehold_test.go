package infinity

import "testing"

// TestZoneHoldRoundTrip is the regression guard for the read/write mismatch
// described in zonehold.go.  Setting a zone's hold must be observable by
// reading that same zone, and must not disturb any other zone.
func TestZoneHoldRoundTrip(t *testing.T) {
	for zone := 1; zone <= 8; zone++ {
		for _, hold := range []bool{true, false} {
			// Start from the opposite state with every other zone flipped, so a
			// mask that is too wide (the `(1<<zone)-1` bug) shows up as
			// collateral damage rather than passing by luck.
			var start uint8 = 0xff
			if hold {
				start = 0x00
			}

			got := SetZoneHold(start, zone, hold)

			if ZoneHeld(got, zone) != hold {
				t.Errorf("zone %d: set hold=%v, read back %v", zone, hold, ZoneHeld(got, zone))
			}

			for other := 1; other <= 8; other++ {
				if other == zone {
					continue
				}
				if ZoneHeld(got, other) != ZoneHeld(start, other) {
					t.Errorf("zone %d set hold=%v disturbed zone %d: %v -> %v",
						zone, hold, other, ZoneHeld(start, other), ZoneHeld(got, other))
				}
			}
		}
	}
}

// TestZoneHoldBit pins the wire encoding: zone 1 is the low bit.  This is the
// assertion that fails outright under the old `1<<zone-1` expression.
func TestZoneHoldBit(t *testing.T) {
	want := []uint8{0x01, 0x02, 0x04, 0x08, 0x10, 0x20, 0x40, 0x80}
	for i, w := range want {
		zone := i + 1
		if got := ZoneHoldBit(zone); got != w {
			t.Errorf("ZoneHoldBit(%d) = %#02x, want %#02x", zone, got, w)
		}
	}
}

// TestZoneHeldDecodesRealBitfield checks decoding of a bitfield with several
// zones held at once, which is the case single-zone testing never exercises.
func TestZoneHeldDecodesRealBitfield(t *testing.T) {
	// Zones 1, 3 and 6 held.
	const bits uint8 = 0x01 | 0x04 | 0x20

	for zone, want := range map[int]bool{
		1: true, 2: false, 3: true, 4: false,
		5: false, 6: true, 7: false, 8: false,
	} {
		if got := ZoneHeld(bits, zone); got != want {
			t.Errorf("ZoneHeld(%#02x, %d) = %v, want %v", bits, zone, got, want)
		}
	}
}
