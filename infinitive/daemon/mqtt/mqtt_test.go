package mqtt

import (
	"testing"
	"time"

	"github.com/montjoy/bryant-carrier-ha/infinitive/daemon/infinity"
)

func boolPtr(b bool) *bool { return &b }

// TestHaModeMapsReadOnlyModesToHeat is the regression guard for the bug that
// made this stack look broken: the thermostat reports "electric" or "heatpump"
// when those sources are selected at the wall unit, and the old integration let
// them fall through to "off", so a running system displayed as Off.
func TestHaModeMapsReadOnlyModesToHeat(t *testing.T) {
	for _, mode := range []string{"electric", "heatpump"} {
		got, ok := haMode(mode)
		if !ok {
			t.Fatalf("haMode(%q) reported not-ok", mode)
		}
		if got != "heat" {
			t.Errorf("haMode(%q) = %q, want %q", mode, got, "heat")
		}
	}
}

func TestHaModeMapping(t *testing.T) {
	cases := map[string]string{
		"off":  "off",
		"heat": "heat",
		"cool": "cool",
		"auto": "heat_cool",
	}
	for in, want := range cases {
		got, ok := haMode(in)
		if !ok || got != want {
			t.Errorf("haMode(%q) = %q,%v; want %q,true", in, got, ok, want)
		}
	}

	if _, ok := haMode("unknown"); ok {
		t.Error(`haMode("unknown") should report not-ok so the field is omitted rather than published wrong`)
	}
}

func TestInfinitiveModeRoundTrip(t *testing.T) {
	// Every mode Home Assistant can send must map back to something writable.
	for _, haValue := range []string{"off", "heat", "cool", "heat_cool"} {
		infMode, ok := infinitiveMode(haValue)
		if !ok {
			t.Fatalf("infinitiveMode(%q) reported not-ok", haValue)
		}
		if _, writable := infinity.StringModeToRaw(infMode); !writable {
			t.Errorf("infinitiveMode(%q) = %q, which is not writable to the bus", haValue, infMode)
		}
		back, _ := haMode(infMode)
		if back != haValue {
			t.Errorf("round trip %q -> %q -> %q", haValue, infMode, back)
		}
	}
}

func TestFanModeMapping(t *testing.T) {
	// Home Assistant says "medium", the bus says "med".
	if got, ok := haFanMode("med"); !ok || got != "medium" {
		t.Errorf(`haFanMode("med") = %q,%v; want "medium",true`, got, ok)
	}
	if got, ok := infinitiveFanMode("medium"); !ok || got != "med" {
		t.Errorf(`infinitiveFanMode("medium") = %q,%v; want "med",true`, got, ok)
	}
	for _, m := range []string{"auto", "low", "high"} {
		if got, ok := infinitiveFanMode(m); !ok || got != m {
			t.Errorf("infinitiveFanMode(%q) = %q,%v", m, got, ok)
		}
	}
}

// TestBuildStateNullsInapplicableSetpoints covers the mechanism that lets one
// static discovery config behave like a dynamic supported_features: the
// setpoints that do not apply to the current mode must be published as the
// literal "None" so Home Assistant clears them.
func TestBuildStateNullsInapplicableSetpoints(t *testing.T) {
	base := func(mode string) *infinity.TStatZoneConfig {
		return &infinity.TStatZoneConfig{
			Mode: mode, HeatSetpoint: 68, CoolSetpoint: 76,
			CurrentTemp: 71, Hold: boolPtr(false),
		}
	}

	b := &Bridge{}

	t.Run("heat shows a single setpoint", func(t *testing.T) {
		s := b.buildState(base("heat"), infinity.AirHandler{}, false, infinity.HeatPump{}, false)
		if s.Temperature != 68 {
			t.Errorf("temperature = %v, want 68", s.Temperature)
		}
		if s.TemperatureLow != payloadNone || s.TemperatureHigh != payloadNone {
			t.Errorf("low/high = %v/%v, want both %q", s.TemperatureLow, s.TemperatureHigh, payloadNone)
		}
	})

	t.Run("cool shows the cool setpoint", func(t *testing.T) {
		s := b.buildState(base("cool"), infinity.AirHandler{}, false, infinity.HeatPump{}, false)
		if s.Temperature != 76 {
			t.Errorf("temperature = %v, want 76", s.Temperature)
		}
		if s.TemperatureLow != payloadNone || s.TemperatureHigh != payloadNone {
			t.Errorf("low/high = %v/%v, want both %q", s.TemperatureLow, s.TemperatureHigh, payloadNone)
		}
	})

	t.Run("auto shows a range", func(t *testing.T) {
		s := b.buildState(base("auto"), infinity.AirHandler{}, false, infinity.HeatPump{}, false)
		if s.Temperature != payloadNone {
			t.Errorf("temperature = %v, want %q", s.Temperature, payloadNone)
		}
		if s.TemperatureLow != 68 || s.TemperatureHigh != 76 {
			t.Errorf("low/high = %v/%v, want 68/76", s.TemperatureLow, s.TemperatureHigh)
		}
	})

	t.Run("heatpump-only behaves like heat", func(t *testing.T) {
		s := b.buildState(base("heatpump"), infinity.AirHandler{}, false, infinity.HeatPump{}, false)
		if s.Mode != "heat" {
			t.Errorf("mode = %q, want heat", s.Mode)
		}
		if s.Temperature != 68 {
			t.Errorf("temperature = %v, want 68", s.Temperature)
		}
	})
}

func TestHaAction(t *testing.T) {
	cfg := func(mode string, stage uint8, cur, heat, cool uint8) *infinity.TStatZoneConfig {
		return &infinity.TStatZoneConfig{
			Mode: mode, Stage: stage, CurrentTemp: cur,
			HeatSetpoint: heat, CoolSetpoint: cool,
		}
	}

	cases := []struct {
		name    string
		cfg     *infinity.TStatZoneConfig
		ah      infinity.AirHandler
		ahFresh bool
		want    string
	}{
		{"off", cfg("off", 0, 70, 68, 76), infinity.AirHandler{}, false, "off"},
		{"heating", cfg("heat", 1, 66, 68, 76), infinity.AirHandler{}, false, "heating"},
		{"cooling", cfg("cool", 2, 78, 68, 76), infinity.AirHandler{}, false, "cooling"},
		{"electric heat is heating", cfg("electric", 1, 66, 68, 76), infinity.AirHandler{}, false, "heating"},
		{"auto above cool setpoint is cooling", cfg("auto", 1, 78, 68, 76), infinity.AirHandler{}, false, "cooling"},
		{"auto below heat setpoint is heating", cfg("auto", 1, 64, 68, 76), infinity.AirHandler{}, false, "heating"},
		// The old integration reported idle here, hiding fan-only operation.
		{"fan only", cfg("heat", 0, 70, 68, 76), infinity.AirHandler{BlowerRPM: 700}, true, "fan"},
		{"idle", cfg("heat", 0, 70, 68, 76), infinity.AirHandler{BlowerRPM: 0}, true, "idle"},
		{"stale air handler cannot claim fan", cfg("heat", 0, 70, 68, 76), infinity.AirHandler{BlowerRPM: 700}, false, "idle"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := haAction(tc.cfg, tc.ah, tc.ahFresh); got != tc.want {
				t.Errorf("haAction = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestBuildStateOmitsStaleEquipment checks that a device that has gone quiet on
// the bus stops contributing values, rather than reporting its last reading
// forever the way the original cache did.
func TestBuildStateOmitsStaleEquipment(t *testing.T) {
	now := time.Now()
	cfg := &infinity.TStatZoneConfig{Mode: "heat", HeatSetpoint: 68, CoolSetpoint: 76}
	ah := infinity.AirHandler{BlowerRPM: 700, AirFlowCFM: 1200, ElecHeat: true, LastUpdated: &now}
	hp := infinity.HeatPump{CoilTemp: 28.8125, OutsideTemp: 31.375, Stage: 2, LastUpdated: &now}

	b := &Bridge{}

	fresh := b.buildState(cfg, ah, true, hp, true)
	if fresh.BlowerRPM == nil || *fresh.BlowerRPM != 700 {
		t.Errorf("fresh blower_rpm = %v, want 700", fresh.BlowerRPM)
	}
	if fresh.ElectricHeat != payloadOn {
		t.Errorf("fresh electric_heat = %q, want %q", fresh.ElectricHeat, payloadOn)
	}
	if fresh.HeatPumpCoilTemperature == nil || *fresh.HeatPumpCoilTemperature != 28.8 {
		t.Errorf("fresh coil temp = %v, want 28.8 (rounded)", fresh.HeatPumpCoilTemperature)
	}

	stale := b.buildState(cfg, ah, false, hp, false)
	if stale.BlowerRPM != nil || stale.AirflowCFM != nil || stale.ElectricHeat != "" {
		t.Errorf("stale air handler still reported values: %+v", stale)
	}
	if stale.HeatPumpCoilTemperature != nil || stale.HeatPumpStage != nil {
		t.Errorf("stale heat pump still reported values: %+v", stale)
	}
}

func TestIsFresh(t *testing.T) {
	if isFresh(nil) {
		t.Error("a device never heard from must not be fresh")
	}
	old := time.Now().Add(-2 * staleAfter)
	if isFresh(&old) {
		t.Error("a device last heard from long ago must not be fresh")
	}
	now := time.Now()
	if !isFresh(&now) {
		t.Error("a device just heard from must be fresh")
	}
}

func TestParseSetpointClamps(t *testing.T) {
	b := &Bridge{cfg: Config{MinSetpoint: 45, MaxSetpoint: 95}}

	cases := map[string]uint8{
		"72.0": 72, // what Home Assistant actually sends
		"72.6": 73, // rounds rather than truncating
		"10":   45, // clamped to the floor
		"200":  95, // clamped to the ceiling
	}
	for in, want := range cases {
		got, err := b.parseSetpoint(in)
		if err != nil {
			t.Errorf("parseSetpoint(%q) errored: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseSetpoint(%q) = %d, want %d", in, got, want)
		}
	}

	if _, err := b.parseSetpoint("warm"); err == nil {
		t.Error("parseSetpoint should reject a non-numeric payload")
	}
}

func TestNormalizeBroker(t *testing.T) {
	// Home Assistant's MQTT service hands back a bare host and port.
	if got := normalizeBroker("core-mosquitto:1883"); got != "tcp://core-mosquitto:1883" {
		t.Errorf("normalizeBroker = %q", got)
	}
	// An explicit scheme is left alone so TLS can be configured.
	for _, url := range []string{"tcp://host:1883", "ssl://host:8883", "ws://host:9001"} {
		if got := normalizeBroker(url); got != url {
			t.Errorf("normalizeBroker(%q) = %q, want unchanged", url, got)
		}
	}
}

func TestTopicLayout(t *testing.T) {
	b := &Bridge{cfg: Config{TopicPrefix: "infinitive", NodeID: "infinitive", DiscoveryPrefix: "homeassistant"}}

	cases := map[string]string{
		b.stateTopic():                            "infinitive/infinitive/state",
		b.availabilityTopic():                     "infinitive/infinitive/availability",
		b.sourceAvailabilityTopic("heatpump"):     "infinitive/infinitive/heatpump/availability",
		b.commandTopic("mode"):                    "infinitive/infinitive/set/mode",
		b.discoveryTopic("climate", "thermostat"): "homeassistant/climate/infinitive/thermostat/config",
		b.discoveryTopic("sensor", "blower_rpm"):  "homeassistant/sensor/infinitive/blower_rpm/config",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("topic = %q, want %q", got, want)
		}
	}

	// A trailing slash on either prefix must not produce a doubled separator.
	b2 := &Bridge{cfg: Config{TopicPrefix: "infinitive/", NodeID: "n", DiscoveryPrefix: "homeassistant/"}}
	if got := b2.stateTopic(); got != "infinitive/n/state" {
		t.Errorf("stateTopic with trailing slash = %q", got)
	}
	if got := b2.discoveryTopic("climate", "thermostat"); got != "homeassistant/climate/n/thermostat/config" {
		t.Errorf("discoveryTopic with trailing slash = %q", got)
	}
}

// TestCommandTopicsAreAllHandled makes sure every command topic advertised in
// the discovery payload has a matching case in the command handler.  Without
// this, adding a control to discovery and forgetting to wire it up would fail
// silently at runtime.
func TestCommandTopicsAreAllHandled(t *testing.T) {
	b := &Bridge{cfg: DefaultConfig()}
	payload := b.climatePayload()

	advertised := []string{
		payload.ModeCommandTopic,
		payload.FanModeCommandTopic,
		payload.PresetModeCommandTopic,
		payload.TemperatureCommandTopic,
		payload.TemperatureLowCommandTopic,
		payload.TemperatureHighCommandTopic,
	}

	handled := map[string]bool{
		"mode": true, "fan_mode": true, "preset_mode": true,
		"temperature": true, "temperature_low": true, "temperature_high": true,
	}

	for _, topic := range advertised {
		if topic == "" {
			t.Fatal("discovery advertises an empty command topic")
		}
		field := topic[len(b.commandPrefix()):]
		if !handled[field] {
			t.Errorf("discovery advertises command topic %q but handleCommand has no case for %q", topic, field)
		}
	}
}

// TestSensorSpecsAreDistinct guards against a copy-paste duplicate producing
// two entities with the same unique id, which Home Assistant would reject.
func TestSensorSpecsAreDistinct(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range sensorSpecs("°F") {
		if seen[s.Field] {
			t.Errorf("duplicate sensor field %q", s.Field)
		}
		seen[s.Field] = true

		if s.Component != "sensor" && s.Component != "binary_sensor" {
			t.Errorf("sensor %q has unexpected component %q", s.Field, s.Component)
		}
		if s.Name == "" {
			t.Errorf("sensor %q has no name", s.Field)
		}
	}
}
