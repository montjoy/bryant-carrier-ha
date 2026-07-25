package mqtt

import "fmt"

// Home Assistant MQTT discovery payloads.
//
// Everything the thermostat reports is published to a single retained JSON
// state topic, and each entity picks its field out with a value template.  That
// keeps updates atomic (Home Assistant never sees a half-applied change) and
// keeps the broker traffic to one message per actual change rather than one per
// attribute.

// device is the Home Assistant device registry entry every entity attaches to,
// so the climate entity and all the sensors group under one device.
type device struct {
	Identifiers  []string `json:"identifiers"`
	Name         string   `json:"name"`
	Manufacturer string   `json:"manufacturer"`
	Model        string   `json:"model"`
	SwVersion    string   `json:"sw_version,omitempty"`
}

// availabilityRef is one entry in an entity's availability list.
type availabilityRef struct {
	Topic               string `json:"topic"`
	PayloadAvailable    string `json:"payload_available,omitempty"`
	PayloadNotAvailable string `json:"payload_not_available,omitempty"`
}

// climateDiscovery is the discovery payload for the thermostat itself.
//
// Note that every temperature topic is configured -- single setpoint *and*
// high/low.  Home Assistant derives supported_features from which topics exist,
// so this sets both TARGET_TEMPERATURE and TARGET_TEMPERATURE_RANGE.  The
// frontend then decides which control to draw based on whether the
// corresponding attribute is null, and the publisher nulls the ones that do not
// apply to the current mode (see buildState).  That reproduces the old
// integration's mode-dependent behaviour without needing custom code.
type climateDiscovery struct {
	// Name is deliberately null so the entity takes the device's name rather
	// than doubling it up, e.g. "Bryant/Carrier HVAC Thermostat".
	Name     *string `json:"name"`
	UniqueID string  `json:"unique_id"`
	ObjectID string  `json:"object_id,omitempty"`
	Device   device  `json:"device"`

	Availability     []availabilityRef `json:"availability"`
	AvailabilityMode string            `json:"availability_mode,omitempty"`

	TemperatureUnit string  `json:"temperature_unit"`
	MinTemp         float64 `json:"min_temp"`
	MaxTemp         float64 `json:"max_temp"`
	TempStep        float64 `json:"temp_step"`
	Precision       float64 `json:"precision"`

	Modes             []string `json:"modes"`
	ModeStateTopic    string   `json:"mode_state_topic"`
	ModeStateTemplate string   `json:"mode_state_template"`
	ModeCommandTopic  string   `json:"mode_command_topic"`

	ActionTopic    string `json:"action_topic"`
	ActionTemplate string `json:"action_template"`

	CurrentTemperatureTopic    string `json:"current_temperature_topic"`
	CurrentTemperatureTemplate string `json:"current_temperature_template"`
	CurrentHumidityTopic       string `json:"current_humidity_topic"`
	CurrentHumidityTemplate    string `json:"current_humidity_template"`

	TemperatureStateTopic    string `json:"temperature_state_topic"`
	TemperatureStateTemplate string `json:"temperature_state_template"`
	TemperatureCommandTopic  string `json:"temperature_command_topic"`

	TemperatureLowStateTopic    string `json:"temperature_low_state_topic"`
	TemperatureLowStateTemplate string `json:"temperature_low_state_template"`
	TemperatureLowCommandTopic  string `json:"temperature_low_command_topic"`

	TemperatureHighStateTopic    string `json:"temperature_high_state_topic"`
	TemperatureHighStateTemplate string `json:"temperature_high_state_template"`
	TemperatureHighCommandTopic  string `json:"temperature_high_command_topic"`

	FanModes             []string `json:"fan_modes"`
	FanModeStateTopic    string   `json:"fan_mode_state_topic"`
	FanModeStateTemplate string   `json:"fan_mode_state_template"`
	FanModeCommandTopic  string   `json:"fan_mode_command_topic"`

	PresetModes             []string `json:"preset_modes"`
	PresetModeStateTopic    string   `json:"preset_mode_state_topic"`
	PresetModeValueTemplate string   `json:"preset_mode_value_template"`
	PresetModeCommandTopic  string   `json:"preset_mode_command_topic"`
}

// entityDiscovery is the discovery payload shared by the sensor and
// binary_sensor entities, all of which are read-only views on the same state
// topic.
type entityDiscovery struct {
	Name     string `json:"name"`
	UniqueID string `json:"unique_id"`
	ObjectID string `json:"object_id,omitempty"`
	Device   device `json:"device"`

	Availability     []availabilityRef `json:"availability"`
	AvailabilityMode string            `json:"availability_mode,omitempty"`

	StateTopic    string `json:"state_topic"`
	ValueTemplate string `json:"value_template"`

	DeviceClass       string `json:"device_class,omitempty"`
	StateClass        string `json:"state_class,omitempty"`
	UnitOfMeasurement string `json:"unit_of_measurement,omitempty"`
	Icon              string `json:"icon,omitempty"`
	EntityCategory    string `json:"entity_category,omitempty"`

	PayloadOn  string `json:"payload_on,omitempty"`
	PayloadOff string `json:"payload_off,omitempty"`
}

// source identifies which piece of equipment an entity's data comes from,
// which determines the extra availability topic it depends on.
type source int

const (
	sourceThermostat source = iota
	sourceAirHandler
	sourceHeatPump
)

// sensorSpec describes one read-only entity.  Field is both the key in the
// state JSON and the suffix of the entity's unique id, so the two can never
// drift apart.
type sensorSpec struct {
	Field       string
	Name        string
	Component   string // "sensor" or "binary_sensor"
	DeviceClass string
	StateClass  string
	Unit        string
	Icon        string
	Source      source
}

// sensorSpecs is the full set of read-only entities.  Adding a reading is a
// single entry here plus the matching field in buildState.
func sensorSpecs(tempUnit string) []sensorSpec {
	return []sensorSpec{
		{
			Field: "outdoor_temperature", Name: "Outdoor temperature", Component: "sensor",
			DeviceClass: "temperature", StateClass: "measurement", Unit: tempUnit,
			Source: sourceThermostat,
		},
		{
			Field: "target_humidity", Name: "Target humidity", Component: "sensor",
			DeviceClass: "humidity", StateClass: "measurement", Unit: "%",
			Source: sourceThermostat,
		},
		{
			Field: "hold_duration", Name: "Hold duration", Component: "sensor",
			DeviceClass: "duration", StateClass: "measurement", Unit: "min",
			Source: sourceThermostat,
		},
		{
			Field: "stage", Name: "Stage", Component: "sensor",
			StateClass: "measurement", Icon: "mdi:stairs",
			Source: sourceThermostat,
		},
		{
			Field: "blower_rpm", Name: "Blower RPM", Component: "sensor",
			StateClass: "measurement", Unit: "rpm", Icon: "mdi:fan",
			Source: sourceAirHandler,
		},
		{
			Field: "airflow_cfm", Name: "Airflow", Component: "sensor",
			StateClass: "measurement", Unit: "ft³/min", Icon: "mdi:weather-windy",
			Source: sourceAirHandler,
		},
		{
			Field: "electric_heat", Name: "Electric heat", Component: "binary_sensor",
			DeviceClass: "heat",
			Source:      sourceAirHandler,
		},
		{
			Field: "heatpump_coil_temperature", Name: "Heat pump coil temperature", Component: "sensor",
			DeviceClass: "temperature", StateClass: "measurement", Unit: tempUnit,
			Source: sourceHeatPump,
		},
		{
			Field: "heatpump_outside_temperature", Name: "Heat pump outside temperature", Component: "sensor",
			DeviceClass: "temperature", StateClass: "measurement", Unit: tempUnit,
			Source: sourceHeatPump,
		},
		{
			Field: "heatpump_stage", Name: "Heat pump stage", Component: "sensor",
			StateClass: "measurement", Icon: "mdi:stairs",
			Source: sourceHeatPump,
		},
	}
}

// availabilityFor builds the availability list for an entity.  Everything
// depends on the daemon being up; air handler and heat pump readings
// additionally depend on that device still being heard from on the bus, since
// those values are snooped passively and would otherwise linger forever after
// the equipment goes quiet.
func (b *Bridge) availabilityFor(s source) []availabilityRef {
	refs := []availabilityRef{{Topic: b.availabilityTopic()}}
	switch s {
	case sourceAirHandler:
		refs = append(refs, availabilityRef{Topic: b.sourceAvailabilityTopic("airhandler")})
	case sourceHeatPump:
		refs = append(refs, availabilityRef{Topic: b.sourceAvailabilityTopic("heatpump")})
	}
	return refs
}

func (b *Bridge) device() device {
	return device{
		Identifiers:  []string{b.cfg.NodeID},
		Name:         b.cfg.DeviceName,
		Manufacturer: "Carrier/Bryant",
		Model:        "Infinity/Evolution (ABCD bus)",
		SwVersion:    b.cfg.Version,
	}
}

func (b *Bridge) climatePayload() climateDiscovery {
	state := b.stateTopic()
	tpl := func(field string) string { return fmt.Sprintf("{{ value_json.%s }}", field) }

	return climateDiscovery{
		Name:     nil,
		UniqueID: b.cfg.NodeID + "_thermostat",
		ObjectID: b.cfg.NodeID + "_thermostat",
		Device:   b.device(),

		Availability:     b.availabilityFor(sourceThermostat),
		AvailabilityMode: "all",

		TemperatureUnit: b.tempUnitLetter(),
		MinTemp:         float64(b.cfg.MinSetpoint),
		MaxTemp:         float64(b.cfg.MaxSetpoint),
		TempStep:        1,
		Precision:       1,

		// "heat_cool" is not in the documented default mode list, but the
		// discovery schema validates this key with cv.ensure_list rather than
		// restricting it, and heat_cool is a real HVACMode.  It is what makes
		// the frontend draw a dual setpoint for the thermostat's auto mode.  If
		// a future Home Assistant tightens this, "auto" is the fallback.
		Modes:             []string{"off", "heat", "cool", "heat_cool"},
		ModeStateTopic:    state,
		ModeStateTemplate: tpl("mode"),
		ModeCommandTopic:  b.commandTopic("mode"),

		ActionTopic:    state,
		ActionTemplate: tpl("action"),

		CurrentTemperatureTopic:    state,
		CurrentTemperatureTemplate: tpl("current_temperature"),
		CurrentHumidityTopic:       state,
		CurrentHumidityTemplate:    tpl("current_humidity"),

		TemperatureStateTopic:    state,
		TemperatureStateTemplate: tpl("temperature"),
		TemperatureCommandTopic:  b.commandTopic("temperature"),

		TemperatureLowStateTopic:    state,
		TemperatureLowStateTemplate: tpl("temperature_low"),
		TemperatureLowCommandTopic:  b.commandTopic("temperature_low"),

		TemperatureHighStateTopic:    state,
		TemperatureHighStateTemplate: tpl("temperature_high"),
		TemperatureHighCommandTopic:  b.commandTopic("temperature_high"),

		FanModes:             []string{"auto", "low", "medium", "high"},
		FanModeStateTopic:    state,
		FanModeStateTemplate: tpl("fan_mode"),
		FanModeCommandTopic:  b.commandTopic("fan_mode"),

		// "none" is implicit in Home Assistant and must not be listed here.
		PresetModes:             []string{presetHold},
		PresetModeStateTopic:    state,
		PresetModeValueTemplate: tpl("preset_mode"),
		PresetModeCommandTopic:  b.commandTopic("preset_mode"),
	}
}

func (b *Bridge) sensorPayload(s sensorSpec) entityDiscovery {
	e := entityDiscovery{
		Name:     s.Name,
		UniqueID: b.cfg.NodeID + "_" + s.Field,
		ObjectID: b.cfg.NodeID + "_" + s.Field,
		Device:   b.device(),

		Availability:     b.availabilityFor(s.Source),
		AvailabilityMode: "all",

		StateTopic:    b.stateTopic(),
		ValueTemplate: fmt.Sprintf("{{ value_json.%s }}", s.Field),

		DeviceClass:       s.DeviceClass,
		StateClass:        s.StateClass,
		UnitOfMeasurement: s.Unit,
		Icon:              s.Icon,
	}

	if s.Component == "binary_sensor" {
		// Binary sensors have no state class, and need explicit payloads since
		// the value comes out of a JSON template rather than a raw topic.
		e.StateClass = ""
		e.PayloadOn = payloadOn
		e.PayloadOff = payloadOff
	}

	return e
}
