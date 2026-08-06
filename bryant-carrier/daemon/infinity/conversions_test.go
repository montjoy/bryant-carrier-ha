package infinity

import "testing"

// TestOffIsFour is the regression guard for a mode that could be read but never
// written.  Captured from a Bryant system with a variable speed furnace and a
// heat pump: pressing Off at the wall unit while in cool moved byte 19 of table
// 003b02 from 0x01 to 0x04, and the thermostat then shut the furnace down over
// the bus.  Upstream read 4 as "heat pump only" and wrote 5 for off, so
// selecting Off produced a frame the thermostat ACKed and ignored, while the
// real off state came back as heat.
func TestOffIsFour(t *testing.T) {
	if got := RawModeToString(4); got != "off" {
		t.Errorf("RawModeToString(4) = %q, want off", got)
	}

	raw, ok := StringModeToRaw("off")
	if !ok {
		t.Fatal(`StringModeToRaw("off") reported not-ok`)
	}
	if raw != 4 {
		t.Errorf(`StringModeToRaw("off") = %d, want 4`, raw)
	}
}

// The observed value for cool, from the same capture, pinning the one other
// mode the frames confirm.
func TestCoolIsOne(t *testing.T) {
	if got := RawModeToString(1); got != "cool" {
		t.Errorf("RawModeToString(1) = %q, want cool", got)
	}
}

// Every writable mode has to survive the trip back, or the card will disagree
// with the equipment right after a successful write.
func TestWritableModesRoundTrip(t *testing.T) {
	for _, mode := range []string{"heat", "cool", "auto", "off"} {
		raw, ok := StringModeToRaw(mode)
		if !ok {
			t.Fatalf("StringModeToRaw(%q) reported not-ok", mode)
		}
		if got := RawModeToString(raw); got != mode {
			t.Errorf("%q wrote %d, which reads back as %q", mode, raw, got)
		}
	}
}

// Anything unrecognized has to be rejected rather than coerced.  The original
// bug here mapped every unknown string to off, so a typo shut the system down.
func TestStringModeToRawRejectsUnwritableModes(t *testing.T) {
	for _, mode := range []string{"", "heat_cool", "electric", "heatpump", "typo", "OFF"} {
		if _, ok := StringModeToRaw(mode); ok {
			t.Errorf("StringModeToRaw(%q) accepted a mode that cannot be written", mode)
		}
	}
}

func TestFanModeRoundTrip(t *testing.T) {
	for _, mode := range []string{"auto", "low", "med", "high"} {
		raw, ok := StringFanModeToRaw(mode)
		if !ok {
			t.Fatalf("StringFanModeToRaw(%q) reported not-ok", mode)
		}
		if got := RawFanModeToString(raw); got != mode {
			t.Errorf("%q wrote %d, which reads back as %q", mode, raw, got)
		}
	}
}
