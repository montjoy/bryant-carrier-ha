// Package mqtt bridges the Infinity bus to Home Assistant over MQTT.
//
// It publishes Home Assistant MQTT discovery configs so the thermostat and its
// sensors appear automatically, publishes state as a single retained JSON
// document, and subscribes to command topics to drive the equipment.  No
// custom Home Assistant component is involved.
package mqtt

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	"github.com/montjoy/bryant-carrier-ha/bryant-carrier/daemon/infinity"
	log "github.com/sirupsen/logrus"
)

const (
	payloadOnline  = "online"
	payloadOffline = "offline"
	payloadOn      = "ON"
	payloadOff     = "OFF"

	presetHold = "hold"
	presetNone = "none"

	// payloadNone is the literal Home Assistant treats as "clear this value".
	// It is how a setpoint that does not apply to the current mode is hidden.
	payloadNone = "None"

	// publishInterval is how often state is rebuilt and compared.  Publishing
	// only happens when something actually changed, so this is cheap.
	publishInterval = 2 * time.Second

	// staleAfter is how long a passively snooped device may go unheard before
	// its entities are marked unavailable.  Nothing on the bus announces that a
	// device has gone away, so this is the only signal available.
	staleAfter = 5 * time.Minute

	// commandSettle is how long to wait after a write before republishing, to
	// give the once-per-second poller time to read the new value back.
	commandSettle = 1500 * time.Millisecond
)

// Config holds everything the bridge needs.  Zero values are not useful; use
// DefaultConfig and override.
type Config struct {
	Broker   string // host:port, or a full scheme://host:port URL
	Username string
	Password string
	ClientID string

	// DiscoveryPrefix is the Home Assistant discovery root, "homeassistant"
	// unless it has been changed in the MQTT integration options.
	DiscoveryPrefix string
	// TopicPrefix is the root for this daemon's own state and command topics.
	TopicPrefix string
	// NodeID namespaces this daemon's entities; it also forms the device
	// identifier, so changing it creates a new device in Home Assistant.
	NodeID     string
	DeviceName string
	Version    string

	// Zone is the zone exposed to Home Assistant.
	Zone int

	MinSetpoint uint8
	MaxSetpoint uint8
	// MinSetpointSpread is the smallest gap allowed between the heat and cool
	// setpoints.  The equipment enforces a deadband of its own; nudging the
	// other setpoint here keeps Home Assistant and the thermostat in agreement
	// instead of letting the thermostat silently override what was requested.
	MinSetpointSpread uint8
}

func DefaultConfig() Config {
	return Config{
		DiscoveryPrefix: "homeassistant",
		TopicPrefix:     "bryant_carrier",
		// NodeID becomes part of every entity's object id and the Home Assistant
		// device identifier, so it uses underscores rather than hyphens to match
		// entity id conventions: climate.bryant_carrier_thermostat.
		NodeID:            "bryant_carrier",
		DeviceName:        "Bryant/Carrier HVAC",
		ClientID:          "bryant_carrier",
		Zone:              1,
		MinSetpoint:       45,
		MaxSetpoint:       95,
		MinSetpointSpread: 2,
	}
}

type Bridge struct {
	cfg    Config
	api    *infinity.Api
	client paho.Client

	mu sync.Mutex
	// lastState is the last state document published, used to suppress
	// no-op publishes.
	lastState []byte
	// lastAvailability tracks the per-source availability last published, so
	// those topics are only written on transition.
	lastAvailability map[string]string
	// discoveredUnit is the temperature unit the discovery configs were built
	// with.  If the thermostat turns out to report a different one, discovery
	// is republished.
	discoveredUnit string
}

func New(cfg Config, api *infinity.Api) *Bridge {
	return &Bridge{
		cfg:              cfg,
		api:              api,
		lastAvailability: make(map[string]string),
	}
}

// ---- topics ----

func (b *Bridge) base() string {
	return strings.TrimSuffix(b.cfg.TopicPrefix, "/") + "/" + b.cfg.NodeID
}

func (b *Bridge) stateTopic() string        { return b.base() + "/state" }
func (b *Bridge) availabilityTopic() string { return b.base() + "/availability" }

func (b *Bridge) sourceAvailabilityTopic(name string) string {
	return b.base() + "/" + name + "/availability"
}

func (b *Bridge) commandPrefix() string        { return b.base() + "/set/" }
func (b *Bridge) commandTopic(s string) string { return b.commandPrefix() + s }
func (b *Bridge) discoveryTopic(component, object string) string {
	return fmt.Sprintf("%s/%s/%s/%s/config",
		strings.TrimSuffix(b.cfg.DiscoveryPrefix, "/"), component, b.cfg.NodeID, object)
}

// tempUnitLetter renders the thermostat's configured unit the way Home
// Assistant wants it.  These systems are Fahrenheit in practice and the
// setpoints cross the bus as whole degrees F, so that is the fallback when the
// settings table has not been read yet or reports something unrecognized.
func (b *Bridge) tempUnitLetter() string {
	// Tolerating a nil api keeps the discovery payloads constructible without a
	// live bus, which is what makes them testable.
	if b.api != nil && b.api.TempUnit() == "celsius" {
		return "C"
	}
	return "F"
}

func (b *Bridge) tempUnitSymbol() string {
	return "°" + b.tempUnitLetter()
}

// ---- lifecycle ----

// Run connects to the broker and publishes until ctx is cancelled.  It does not
// return an error for connection problems: the broker may legitimately start
// after this daemon does, so the client retries in the background and the rest
// of the daemon keeps serving HTTP regardless.
func (b *Bridge) Run(ctx context.Context) {
	opts := paho.NewClientOptions()
	opts.AddBroker(normalizeBroker(b.cfg.Broker))
	opts.SetClientID(b.cfg.ClientID)
	if b.cfg.Username != "" {
		opts.SetUsername(b.cfg.Username)
		opts.SetPassword(b.cfg.Password)
	}
	opts.SetAutoReconnect(true)
	opts.SetConnectRetry(true)
	opts.SetConnectRetryInterval(10 * time.Second)
	opts.SetResumeSubs(true)
	// The will makes Home Assistant mark everything unavailable if this daemon
	// dies without a clean shutdown.
	opts.SetWill(b.availabilityTopic(), payloadOffline, 1, true)
	opts.SetOnConnectHandler(b.onConnect)
	opts.SetConnectionLostHandler(func(_ paho.Client, err error) {
		log.Warnf("mqtt connection lost: %v", err)
	})

	b.client = paho.NewClient(opts)
	log.Infof("connecting to mqtt broker %s", normalizeBroker(b.cfg.Broker))
	// With ConnectRetry set, this token resolves once connected; failures are
	// retried internally rather than surfaced here.
	b.client.Connect()

	ticker := time.NewTicker(publishInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			b.publish()
		case <-ctx.Done():
			b.shutdown()
			return
		}
	}
}

func (b *Bridge) shutdown() {
	if b.client == nil || !b.client.IsConnected() {
		return
	}
	log.Info("marking mqtt entities offline")
	// Publish offline explicitly rather than relying on the will, so a clean
	// stop is reflected immediately instead of after the keepalive timeout.
	b.client.Publish(b.availabilityTopic(), 1, true, payloadOffline).WaitTimeout(2 * time.Second)
	b.client.Disconnect(500)
}

func (b *Bridge) onConnect(c paho.Client) {
	log.Info("mqtt connected, publishing discovery")

	b.publishDiscovery()

	if token := c.Subscribe(b.commandPrefix()+"+", 1, b.handleCommand); token.Wait() && token.Error() != nil {
		log.Errorf("mqtt subscribe failed: %v", token.Error())
	}

	c.Publish(b.availabilityTopic(), 1, true, payloadOnline)

	// Force the next tick to publish even if the state document is unchanged,
	// so a reconnect always leaves the broker holding current retained state.
	b.mu.Lock()
	b.lastState = nil
	b.lastAvailability = make(map[string]string)
	b.mu.Unlock()
}

// publishDiscovery writes the retained discovery config for every entity.
func (b *Bridge) publishDiscovery() {
	unit := b.tempUnitSymbol()

	b.mu.Lock()
	b.discoveredUnit = unit
	b.mu.Unlock()

	b.publishJSON(b.discoveryTopic("climate", "thermostat"), b.climatePayload())

	for _, spec := range sensorSpecs(unit) {
		b.publishJSON(b.discoveryTopic(spec.Component, spec.Field), b.sensorPayload(spec))
	}
}

func (b *Bridge) publishJSON(topic string, payload any) {
	buf, err := json.Marshal(payload)
	if err != nil {
		log.Errorf("failed to marshal payload for %s: %v", topic, err)
		return
	}
	b.client.Publish(topic, 1, true, buf)
	log.Debugf("published discovery to %s", topic)
}

// ---- state publishing ----

func (b *Bridge) publish() {
	if b.client == nil || !b.client.IsConnected() {
		return
	}

	// The thermostat's temperature unit is only known after the settings table
	// has been read, which happens shortly after start.  If it turns out to
	// differ from what discovery was published with, republish so the entities
	// carry the right unit.
	b.mu.Lock()
	unitChanged := b.discoveredUnit != "" && b.discoveredUnit != b.tempUnitSymbol()
	b.mu.Unlock()
	if unitChanged {
		log.Infof("thermostat temperature unit changed to %s, republishing discovery", b.tempUnitSymbol())
		b.publishDiscovery()
	}

	cfg, ok := b.api.GetCachedConfig()
	if !ok || cfg == nil {
		// Nothing polled yet; the thermostat has not answered since start.
		return
	}

	airHandler, ahOK := b.api.GetAirHandler()
	heatPump, hpOK := b.api.GetHeatPump()

	ahFresh := ahOK && isFresh(airHandler.LastUpdated)
	hpFresh := hpOK && isFresh(heatPump.LastUpdated)

	b.publishAvailability("airhandler", ahFresh)
	b.publishAvailability("heatpump", hpFresh)

	state := b.buildState(cfg, airHandler, ahFresh, heatPump, hpFresh)
	buf, err := json.Marshal(state)
	if err != nil {
		log.Errorf("failed to marshal state: %v", err)
		return
	}

	b.mu.Lock()
	unchanged := b.lastState != nil && string(b.lastState) == string(buf)
	if !unchanged {
		b.lastState = buf
	}
	b.mu.Unlock()

	if unchanged {
		return
	}

	b.client.Publish(b.stateTopic(), 1, true, buf)
	log.Debugf("published state: %s", buf)
}

func (b *Bridge) publishAvailability(name string, available bool) {
	payload := payloadOffline
	if available {
		payload = payloadOnline
	}

	b.mu.Lock()
	changed := b.lastAvailability[name] != payload
	if changed {
		b.lastAvailability[name] = payload
	}
	b.mu.Unlock()

	if !changed {
		return
	}

	log.Infof("%s is now %s", name, payload)
	b.client.Publish(b.sourceAvailabilityTopic(name), 1, true, payload)
}

func isFresh(t *time.Time) bool {
	return t != nil && time.Since(*t) < staleAfter
}

// state is the single JSON document every entity reads from.
//
// The setpoint fields are `any` because they carry either a number or the
// string "None", which is how Home Assistant is told a setpoint does not apply.
// They have no omitempty: clearing a setpoint requires actually sending "None",
// whereas an omitted key renders as an empty template result and is ignored.
type state struct {
	Mode       string `json:"mode,omitempty"`
	Action     string `json:"action,omitempty"`
	FanMode    string `json:"fan_mode,omitempty"`
	PresetMode string `json:"preset_mode,omitempty"`

	CurrentTemperature int `json:"current_temperature"`
	CurrentHumidity    int `json:"current_humidity"`

	Temperature     any `json:"temperature"`
	TemperatureLow  any `json:"temperature_low"`
	TemperatureHigh any `json:"temperature_high"`

	OutdoorTemperature int `json:"outdoor_temperature"`
	TargetHumidity     int `json:"target_humidity"`
	HoldDuration       int `json:"hold_duration"`
	Stage              int `json:"stage"`

	// Air handler and heat pump fields are omitted when their device is stale.
	// The entities are already marked unavailable via their availability topic;
	// omitting the values keeps a stale number from being the last thing Home
	// Assistant saw if availability is ever reconfigured.
	BlowerRPM    *int   `json:"blower_rpm,omitempty"`
	AirflowCFM   *int   `json:"airflow_cfm,omitempty"`
	ElectricHeat string `json:"electric_heat,omitempty"`

	HeatPumpCoilTemperature    *float64 `json:"heatpump_coil_temperature,omitempty"`
	HeatPumpOutsideTemperature *float64 `json:"heatpump_outside_temperature,omitempty"`
	HeatPumpStage              *int     `json:"heatpump_stage,omitempty"`
}

func (b *Bridge) buildState(
	cfg *infinity.TStatZoneConfig,
	ah infinity.AirHandler, ahFresh bool,
	hp infinity.HeatPump, hpFresh bool,
) state {
	s := state{
		CurrentTemperature: int(cfg.CurrentTemp),
		CurrentHumidity:    int(cfg.CurrentHumidity),
		OutdoorTemperature: int(cfg.OutdoorTemp),
		TargetHumidity:     int(cfg.TargetHumidity),
		HoldDuration:       int(cfg.HoldDurationMins),
		Stage:              int(cfg.Stage),
	}

	// An unrecognized mode leaves the field out entirely.  The template then
	// renders empty, which Home Assistant ignores, so the entity keeps its last
	// known mode rather than being told something wrong.
	if mode, ok := haMode(cfg.Mode); ok {
		s.Mode = mode
	} else {
		log.Warnf("unrecognized thermostat mode %q, not publishing mode", cfg.Mode)
	}

	if fan, ok := haFanMode(cfg.FanMode); ok {
		s.FanMode = fan
	}

	s.PresetMode = presetNone
	if cfg.Hold != nil && *cfg.Hold {
		s.PresetMode = presetHold
	}

	s.Action = haAction(cfg, ah, ahFresh)

	// Which setpoints apply depends on the mode.  Nulling the others is what
	// makes the frontend draw a single target in heat/cool and a range in
	// heat_cool, from one static discovery config.
	heat := int(cfg.HeatSetpoint)
	cool := int(cfg.CoolSetpoint)
	switch cfg.Mode {
	case "heat", "electric", "heatpump":
		s.Temperature, s.TemperatureLow, s.TemperatureHigh = heat, payloadNone, payloadNone
	case "cool":
		s.Temperature, s.TemperatureLow, s.TemperatureHigh = cool, payloadNone, payloadNone
	default:
		// auto, off, and anything unrecognized: both setpoints are live on the
		// equipment, so show both.
		s.Temperature, s.TemperatureLow, s.TemperatureHigh = payloadNone, heat, cool
	}

	if ahFresh {
		rpm := int(ah.BlowerRPM)
		cfm := int(ah.AirFlowCFM)
		s.BlowerRPM = &rpm
		s.AirflowCFM = &cfm
		s.ElectricHeat = payloadOff
		if ah.ElecHeat {
			s.ElectricHeat = payloadOn
		}
	}

	if hpFresh {
		coil := round1(float64(hp.CoilTemp))
		outside := round1(float64(hp.OutsideTemp))
		stage := int(hp.Stage)
		s.HeatPumpCoilTemperature = &coil
		s.HeatPumpOutsideTemperature = &outside
		s.HeatPumpStage = &stage
	}

	return s
}

func round1(v float64) float64 {
	return math.Round(v*10) / 10
}

// haAction derives the Home Assistant hvac_action.
//
// The old integration only ever reported heating, cooling, or idle, so a system
// running the fan on its own looked idle.
func haAction(cfg *infinity.TStatZoneConfig, ah infinity.AirHandler, ahFresh bool) string {
	if cfg.Mode == "off" {
		return "off"
	}

	if cfg.Stage > 0 {
		switch cfg.Mode {
		case "cool":
			return "cooling"
		case "heat", "electric", "heatpump":
			return "heating"
		case "auto":
			// In auto the reported mode does not say which way the system is
			// driving, so infer it from which setpoint the room has crossed.
			// Ties fall to heating, which matches how these systems behave at
			// the bottom of the deadband.
			if cfg.CurrentTemp >= cfg.CoolSetpoint {
				return "cooling"
			}
			return "heating"
		}
	}

	if ahFresh && ah.BlowerRPM > 0 {
		return "fan"
	}

	return "idle"
}

// ---- mode translation ----

// haMode maps an Infinity mode onto a Home Assistant HVAC mode.
func haMode(mode string) (string, bool) {
	switch mode {
	case "off", "heat", "cool":
		return mode, true
	case "auto":
		return "heat_cool", true
	case "electric", "heatpump":
		// Read-only selections made at the physical thermostat ("electric heat
		// only" / "heat pump only").  The old integration let these fall
		// through to "off", so an actively heating system showed as Off in
		// Home Assistant.  Both are heating.
		return "heat", true
	default:
		return "", false
	}
}

// infinitiveMode is the inverse, for commands arriving from Home Assistant.
func infinitiveMode(mode string) (string, bool) {
	switch mode {
	case "off", "heat", "cool":
		return mode, true
	case "heat_cool", "auto":
		return "auto", true
	default:
		return "", false
	}
}

func haFanMode(mode string) (string, bool) {
	switch mode {
	case "auto", "low", "high":
		return mode, true
	case "med":
		return "medium", true
	default:
		return "", false
	}
}

func infinitiveFanMode(mode string) (string, bool) {
	switch mode {
	case "auto", "low", "high":
		return mode, true
	case "medium", "med":
		return "med", true
	default:
		return "", false
	}
}

// ---- commands ----

func (b *Bridge) handleCommand(_ paho.Client, msg paho.Message) {
	field := strings.TrimPrefix(msg.Topic(), b.commandPrefix())
	payload := strings.TrimSpace(string(msg.Payload()))
	log.Infof("mqtt command %s = %q", field, payload)

	var err error
	switch field {
	case "mode":
		err = b.setMode(payload)
	case "fan_mode":
		err = b.setFanMode(payload)
	case "preset_mode":
		err = b.setPreset(payload)
	case "temperature":
		err = b.setTemperature(payload)
	case "temperature_low":
		err = b.setTemperatureLow(payload)
	case "temperature_high":
		err = b.setTemperatureHigh(payload)
	default:
		err = fmt.Errorf("unknown command topic %q", field)
	}

	if err != nil {
		log.Errorf("mqtt command %s=%q failed: %v", field, payload, err)
		return
	}

	// Nudge a republish once the poller has had time to read the change back,
	// so the UI settles on the real value rather than snapping back to the
	// pre-write state for a couple of seconds.
	time.AfterFunc(commandSettle, b.publish)
}

func (b *Bridge) setMode(payload string) error {
	mode, ok := infinitiveMode(payload)
	if !ok {
		return fmt.Errorf("unsupported hvac mode %q", payload)
	}
	return b.api.SetZoneConfig(b.cfg.Zone, infinity.ZoneUpdate{Mode: &mode})
}

func (b *Bridge) setFanMode(payload string) error {
	mode, ok := infinitiveFanMode(payload)
	if !ok {
		return fmt.Errorf("unsupported fan mode %q", payload)
	}
	return b.api.SetZoneConfig(b.cfg.Zone, infinity.ZoneUpdate{FanMode: &mode})
}

func (b *Bridge) setPreset(payload string) error {
	switch payload {
	case presetHold:
		hold := true
		return b.api.SetZoneConfig(b.cfg.Zone, infinity.ZoneUpdate{Hold: &hold})
	case presetNone, payloadNone, "":
		hold := false
		return b.api.SetZoneConfig(b.cfg.Zone, infinity.ZoneUpdate{Hold: &hold})
	default:
		return fmt.Errorf("unsupported preset %q", payload)
	}
}

// setTemperature handles the single-setpoint control, which is only shown in
// heat or cool mode.  Which physical setpoint it maps to depends on the mode.
func (b *Bridge) setTemperature(payload string) error {
	value, err := b.parseSetpoint(payload)
	if err != nil {
		return err
	}

	cfg, ok := b.api.GetCachedConfig()
	if !ok || cfg == nil {
		return fmt.Errorf("no thermostat state available yet")
	}

	switch cfg.Mode {
	case "heat", "electric", "heatpump":
		return b.setSetpoints(&value, nil)
	case "cool":
		return b.setSetpoints(nil, &value)
	default:
		// heat_cool publishes "None" for this field, so Home Assistant should
		// never send it in auto.  Refuse rather than guess which setpoint the
		// user meant.
		return fmt.Errorf("cannot set a single target temperature in mode %q", cfg.Mode)
	}
}

func (b *Bridge) setTemperatureLow(payload string) error {
	value, err := b.parseSetpoint(payload)
	if err != nil {
		return err
	}
	return b.setSetpoints(&value, nil)
}

func (b *Bridge) setTemperatureHigh(payload string) error {
	value, err := b.parseSetpoint(payload)
	if err != nil {
		return err
	}
	return b.setSetpoints(nil, &value)
}

// setSetpoints applies one or both setpoints, widening the other one if needed
// to preserve the minimum spread.  The setpoint being explicitly requested is
// always honoured; the other is what moves.
func (b *Bridge) setSetpoints(heat, cool *uint8) error {
	cfg, ok := b.api.GetCachedConfig()
	if !ok || cfg == nil {
		return fmt.Errorf("no thermostat state available yet")
	}

	newHeat := cfg.HeatSetpoint
	newCool := cfg.CoolSetpoint
	if heat != nil {
		newHeat = *heat
	}
	if cool != nil {
		newCool = *cool
	}

	spread := b.cfg.MinSetpointSpread
	if newCool < newHeat+spread {
		if cool != nil && heat == nil {
			// Cool was requested: push heat down out of the way.
			if newCool < b.cfg.MinSetpoint+spread {
				newHeat = b.cfg.MinSetpoint
			} else {
				newHeat = newCool - spread
			}
		} else {
			// Heat was requested (or both): push cool up.
			newCool = newHeat + spread
			if newCool > b.cfg.MaxSetpoint {
				newCool = b.cfg.MaxSetpoint
				newHeat = newCool - spread
			}
		}
		log.Infof("adjusted setpoints to preserve %d degree spread: heat=%d cool=%d",
			spread, newHeat, newCool)
	}

	update := infinity.ZoneUpdate{}
	if newHeat != cfg.HeatSetpoint {
		update.HeatSetpoint = &newHeat
	}
	if newCool != cfg.CoolSetpoint {
		update.CoolSetpoint = &newCool
	}
	if update.HeatSetpoint == nil && update.CoolSetpoint == nil {
		return nil
	}

	return b.api.SetZoneConfig(b.cfg.Zone, update)
}

// parseSetpoint accepts the float payloads Home Assistant sends ("72.0") and
// clamps them to the configured range, since the wire format is a single byte
// of whole degrees.
func (b *Bridge) parseSetpoint(payload string) (uint8, error) {
	f, err := strconv.ParseFloat(payload, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid temperature %q: %w", payload, err)
	}

	v := int(math.Round(f))
	if v < int(b.cfg.MinSetpoint) {
		v = int(b.cfg.MinSetpoint)
	}
	if v > int(b.cfg.MaxSetpoint) {
		v = int(b.cfg.MaxSetpoint)
	}
	return uint8(v), nil
}

// normalizeBroker accepts a bare host:port as well as a full URL, since the
// Home Assistant MQTT service discovery hands back a bare host and port.
func normalizeBroker(broker string) string {
	if strings.Contains(broker, "://") {
		return broker
	}
	return "tcp://" + broker
}
