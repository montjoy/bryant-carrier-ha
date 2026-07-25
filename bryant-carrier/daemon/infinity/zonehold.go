package infinity

// ZoneHold in TStatZoneParams is a bitfield with one bit per zone, zone 1 in
// the low bit.  The read and write paths used to compute this mask separately
// and disagreed: the read side wrote `1<<zone-1`, which Go parses as
// `(1<<zone)-1` because << binds tighter than -.  That is correct only for
// zone 1, so on a multi-zone system reading hold state returned a different
// zone's bit than writing it set.  Both paths now go through here.

// ZoneHoldBit returns the ZoneHold mask for a single zone.  Zones are 1-based;
// callers are expected to have validated the range (1-8) already.
func ZoneHoldBit(zone int) uint8 {
	return 1 << (zone - 1)
}

// ZoneHeld reports whether the given zone is currently held.
func ZoneHeld(zoneHold uint8, zone int) bool {
	return zoneHold&ZoneHoldBit(zone) != 0
}

// SetZoneHold returns zoneHold with the given zone's bit set or cleared,
// leaving every other zone's bit untouched.
func SetZoneHold(zoneHold uint8, zone int, hold bool) uint8 {
	if hold {
		return zoneHold | ZoneHoldBit(zone)
	}
	return zoneHold &^ ZoneHoldBit(zone)
}
