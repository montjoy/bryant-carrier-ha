package infinity

func RawModeToString(mode uint8) string {
	switch mode {
	case 0:
		return "heat" // heat source: "system in control"
	case 1:
		return "cool"
	case 2:
		return "auto"
	case 3:
		return "electric" // electric heat only - fan coil system
		// on a furnace system perhaps this is furnace only - untested
	case 4:
		return "heatpump" // heat pump only
	case 5:
		return "off"
	default:
		return "unknown"
	}
}

// StringModeToRaw encodes a writable mode.  The ok result matters: the previous
// version mapped anything unrecognized to 5 ("off"), so a typo in a request
// body silently shut the system down instead of being rejected.  Note that
// "electric" and "heatpump" are read-only -- they can be selected at the
// physical thermostat but not written over the bus.
func StringModeToRaw(mode string) (uint8, bool) {
	switch mode {
	case "heat":
		return 0, true
	case "cool":
		return 1, true
	case "auto":
		return 2, true
	case "off":
		return 5, true
	default:
		return 0, false
	}
}

func RawFanModeToString(mode uint8) string {
	switch mode {
	case 0:
		return "auto"
	case 1:
		return "low"
	case 2:
		return "med"
	case 3:
		return "high"
	default:
		return "unknown"
	}
}

func StringFanModeToRaw(mode string) (uint8, bool) {
	switch mode {
	case "auto":
		return 0, true
	case "low":
		return 1, true
	case "med":
		return 2, true
	case "high":
		return 3, true
	default:
		return 0, false
	}
}

// RawTempUnitToString decodes the TempUnits byte from the thermostat settings
// table.  The encoding is inferred from observed traffic and has only been
// checked against a Fahrenheit thermostat, so anything unrecognized is reported
// as "unknown" rather than guessed at -- consumers are expected to fall back to
// Fahrenheit, which is what these systems ship as in North America.
func RawTempUnitToString(units uint8) string {
	switch units {
	case 0:
		return "fahrenheit"
	case 1:
		return "celsius"
	default:
		return "unknown"
	}
}
