package infinity

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/montjoy/infinitive-ha/infinitive/daemon/internal/cache"
	"github.com/montjoy/infinitive-ha/infinitive/daemon/internal/dispatcher"
	log "github.com/sirupsen/logrus"
)

const (
	blowerCacheKey   = "blower"
	heatpumpCacheKey = "heatpump"
	tstatCacheKey    = "tstat"
)

// settingsRefreshInterval controls how often the thermostat settings table is
// re-read.  The only thing we take from it is the configured temperature unit,
// which changes only when someone reconfigures the physical thermostat, so
// this is deliberately infrequent.
const settingsRefreshInterval = 5 * time.Minute

type Api struct {
	ctx        context.Context
	Bus        *Bus
	dispatcher *dispatcher.Dispatcher
	Cache      *cache.Cache

	mu sync.RWMutex
	// tempUnit is the temperature unit reported by the thermostat.  Empty until
	// the settings table has been read successfully at least once.
	tempUnit string
	// settingsRead is when tempUnit was last refreshed.
	settingsRead time.Time
	// lastSeen records when each snooped device last produced a frame.  This is
	// kept outside the cache on purpose: the cache suppresses updates for equal
	// values, so a device steadily reporting an unchanged reading would never
	// refresh a timestamp stored alongside that value and would look stale.
	lastSeen map[string]time.Time
}

func NewApi(ctx context.Context, device string) (*Api, error) {
	bus, err := NewBus(device)
	if err != nil {
		return nil, err
	}

	dispatcher := dispatcher.New(ctx)

	c := cache.New(dispatcher.BroadcastEvent)
	// Set default values for structs the UI cares about
	c.Update(blowerCacheKey, &AirHandler{})
	c.Update(heatpumpCacheKey, &HeatPump{})

	api := &Api{
		ctx:        ctx,
		Bus:        bus,
		dispatcher: dispatcher,
		Cache:      c,
		lastSeen:   make(map[string]time.Time),
	}
	api.attachSnoops()
	go api.poller()
	return api, nil
}

// markSeen records that a frame was just observed for the given device.
func (a *Api) markSeen(key string) {
	a.mu.Lock()
	a.lastSeen[key] = time.Now()
	a.mu.Unlock()
}

// seenAt returns when a frame was last observed for the given device, or the
// zero time if one never has been.
func (a *Api) seenAt(key string) time.Time {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.lastSeen[key]
}

// TempUnit returns the temperature unit configured on the thermostat, or an
// empty string if the settings table has not been read yet.  Callers should
// treat empty as unknown rather than assuming a default.
func (a *Api) TempUnit() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.tempUnit
}

// refreshSettings re-reads the thermostat settings table if we have never read
// it or the cached copy has aged out.  Failures are not fatal; the next poll
// tick tries again.
func (a *Api) refreshSettings() {
	a.mu.RLock()
	fresh := a.tempUnit != "" && time.Since(a.settingsRead) < settingsRefreshInterval
	a.mu.RUnlock()
	if fresh {
		return
	}

	tss, ok := a.GetTstatSettings()
	if !ok {
		return
	}

	a.mu.Lock()
	a.tempUnit = RawTempUnitToString(tss.TempUnits)
	a.settingsRead = time.Now()
	a.mu.Unlock()
}

func (a *Api) attachSnoops() {
	// Snoop Heat Pump responses
	a.Bus.SnoopResponse(filter(sourceRange(0x5000, 0x51ff), func(frame Frame) {
		if heatPump, ok := a.getHeatPumpRaw(); ok {
			data := frame.data[3:]
			if bytes.Equal(frame.data[0:3], []byte{0x00, 0x3e, 0x01}) {
				heatPump.CoilTemp = float32(binary.BigEndian.Uint16(data[2:4])) / float32(16)
				heatPump.OutsideTemp = float32(binary.BigEndian.Uint16(data[0:2])) / float32(16)
				log.Debugf("heat pump coil temp is: %f", heatPump.CoilTemp)
				log.Debugf("heat pump outside temp is: %f", heatPump.OutsideTemp)
			} else if bytes.Equal(frame.data[0:3], []byte{0x00, 0x3e, 0x02}) {
				heatPump.Stage = data[0] >> 1
				log.Debugf("HP stage is: %d", heatPump.Stage)
			}
			a.markSeen(heatpumpCacheKey)
			a.Cache.Update(heatpumpCacheKey, &heatPump)
		}
	}))

	// Snoop Air Handler responses
	a.Bus.SnoopResponse(filter(sourceRange(0x4000, 0x42ff), func(frame Frame) {
		if airHandler, ok := a.getAirHandlerRaw(); ok {
			data := frame.data[3:]
			if bytes.Equal(frame.data[0:3], []byte{0x00, 0x03, 0x06}) {
				airHandler.BlowerRPM = binary.BigEndian.Uint16(data[1:3])
				log.Debugf("blower RPM is: %d", airHandler.BlowerRPM)
			} else if bytes.Equal(frame.data[0:3], []byte{0x00, 0x03, 0x16}) {
				airHandler.AirFlowCFM = binary.BigEndian.Uint16(data[4:6])
				airHandler.ElecHeat = data[0]&0x03 != 0
				log.Debugf("air flow CFM is: %d", airHandler.AirFlowCFM)
			}
			a.markSeen(blowerCacheKey)
			a.Cache.Update(blowerCacheKey, &airHandler)
		}
	}))
}

func (a *Api) poller() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			a.refreshSettings()
			if c, ok := a.GetConfig(1); ok {
				a.Cache.Update(tstatCacheKey, c)
			}
		case <-a.ctx.Done():
			return
		}
	}
}

type TStatZoneConfig struct {
	TempUnit        string `json:"tempUnit"`
	CurrentTemp     uint8  `json:"currentTemp"`
	CurrentHumidity uint8  `json:"currentHumidity"`
	OutdoorTemp     int8   `json:"outdoorTemp"`
	Mode            string `json:"mode"`
	Stage           uint8  `json:"stage"`
	FanMode         string `json:"fanMode"`
	Hold            *bool  `json:"hold"`
	HeatSetpoint    uint8  `json:"heatSetpoint"`
	CoolSetpoint    uint8  `json:"coolSetpoint"`
	// TargetHumidity and HoldDurationMins live in the zone params table but were
	// never surfaced by the API.  The Home Assistant integration wants both, so
	// they are exposed here rather than in a downstream fork of the daemon.
	TargetHumidity   uint8  `json:"targetHumidity"`
	HoldDurationMins uint16 `json:"holdDurationMins"`
	RawMode          uint8  `json:"rawMode"`
}

type AirHandler struct {
	BlowerRPM  uint16 `json:"blowerRPM"`
	AirFlowCFM uint16 `json:"airFlowCFM"`
	ElecHeat   bool   `json:"elecHeat"`
	// LastUpdated is when a frame from this device was last seen on the bus.  It
	// is stamped on read rather than stored, and omitted entirely if the device
	// has never been heard from.  Consumers use it to decide whether these
	// passively snooped values are still trustworthy: nothing on the bus
	// announces that a device has gone away, so without this a dead air handler
	// reports its last reading forever.
	LastUpdated *time.Time `json:"lastUpdated,omitempty"`
}

type HeatPump struct {
	TempUnit    string  `json:"tempUnit"`
	CoilTemp    float32 `json:"coilTemp"`
	OutsideTemp float32 `json:"outsideTemp"`
	Stage       uint8   `json:"stage"`
	// LastUpdated carries the same meaning as on AirHandler.
	LastUpdated *time.Time `json:"lastUpdated,omitempty"`
}

func (a *Api) GetConfig(zone int) (*TStatZoneConfig, bool) {
	cfg := TStatZoneParams{}
	ok := a.Bus.ReadTable(DevTSTAT, &cfg)
	if !ok {
		return nil, false
	}

	params := TStatCurrentParams{}
	ok = a.Bus.ReadTable(DevTSTAT, &params)
	if !ok {
		return nil, false
	}

	hold := new(bool)
	*hold = ZoneHeld(cfg.ZoneHold, zone)

	return &TStatZoneConfig{
		TempUnit:         a.TempUnit(),
		CurrentTemp:      params.GetZonalField(zone, "CurrentTemp").(uint8),
		CurrentHumidity:  params.GetZonalField(zone, "CurrentHumidity").(uint8),
		OutdoorTemp:      params.OutdoorAirTemp,
		Mode:             RawModeToString(params.Mode & 0xf),
		Stage:            params.Mode >> 5,
		FanMode:          RawFanModeToString(cfg.GetZonalField(zone, "FanMode").(uint8)),
		Hold:             hold,
		HeatSetpoint:     cfg.GetZonalField(zone, "HeatSetpoint").(uint8),
		CoolSetpoint:     cfg.GetZonalField(zone, "CoolSetpoint").(uint8),
		TargetHumidity:   cfg.GetZonalField(zone, "TargetHumidity").(uint8),
		HoldDurationMins: cfg.GetZonalField(zone, "HoldDuration").(uint16),
		RawMode:          params.Mode,
	}, true
}

// ZoneUpdate describes a partial change to a zone's configuration.  A nil field
// is left alone.
type ZoneUpdate struct {
	FanMode      *string
	Hold         *bool
	HeatSetpoint *uint8
	CoolSetpoint *uint8
	Mode         *string
}

// SetZoneConfig applies a partial zone update.  Both the HTTP API and the MQTT
// command handler go through here: the previous generation of this project had
// the write logic living in the web handler, which is how the HTTP and Home
// Assistant paths drifted apart.
func (a *Api) SetZoneConfig(zone int, u ZoneUpdate) error {
	if zone < 1 || zone > 8 {
		return fmt.Errorf("invalid zone: %d", zone)
	}

	params := TStatZoneParams{}
	flags := byte(0)

	if u.FanMode != nil {
		mode, ok := StringFanModeToRaw(*u.FanMode)
		if !ok {
			return fmt.Errorf("invalid fanMode: %s", *u.FanMode)
		}
		params.SetZonalField(zone, "FanMode", mode)
		flags |= 0x01
	}

	if u.Hold != nil {
		// Hold is a bitfield shared across every zone, so the current value has
		// to be read first or setting one zone would clear the rest.
		prior := TStatZoneParams{}
		if !a.Bus.ReadTable(DevTSTAT, &prior) {
			return errors.New("failed to read current zone params")
		}
		params.ZoneHold = SetZoneHold(prior.ZoneHold, zone, *u.Hold)
		flags |= 0x02
	}

	if u.HeatSetpoint != nil {
		params.SetZonalField(zone, "HeatSetpoint", *u.HeatSetpoint)
		flags |= 0x04
	}

	if u.CoolSetpoint != nil {
		params.SetZonalField(zone, "CoolSetpoint", *u.CoolSetpoint)
		flags |= 0x08
	}

	if flags != 0 {
		if !a.UpdateThermostat(params, flags) {
			return errors.New("failed to write zone params")
		}
	}

	// Mode lives in a different table, so it is a separate write.
	if u.Mode != nil {
		raw, ok := StringModeToRaw(*u.Mode)
		if !ok {
			return fmt.Errorf("invalid mode: %s", *u.Mode)
		}
		if !a.UpdateThermostat(TStatCurrentParams{Mode: raw}, 0x10) {
			return errors.New("failed to write mode")
		}
	}

	return nil
}

// GetCachedConfig returns the most recent polled zone configuration without
// touching the bus.  The MQTT publisher uses this so that publishing state
// never adds bus traffic on top of the poller's own reads.
func (a *Api) GetCachedConfig() (*TStatZoneConfig, bool) {
	c, ok := a.Cache.Get(tstatCacheKey).(*TStatZoneConfig)
	return c, ok
}

func (a *Api) GetTstatSettings() (*TStatSettings, bool) {
	tss := TStatSettings{}
	if !a.Bus.ReadTable(DevTSTAT, &tss) {
		return nil, false
	}
	return &tss, true
}

// getAirHandlerRaw returns the cached air handler state without a freshness
// stamp.  The snoop handler uses this so the value written back to the cache
// stays comparable -- the cache dedupes with reflect.DeepEqual, and a timestamp
// that moves on every frame would defeat that.
func (a *Api) getAirHandlerRaw() (AirHandler, bool) {
	b := a.Cache.Get(blowerCacheKey)
	tb, ok := b.(*AirHandler)
	if !ok {
		return AirHandler{}, false
	}
	return *tb, true
}

func (a *Api) GetAirHandler() (AirHandler, bool) {
	ah, ok := a.getAirHandlerRaw()
	if !ok {
		return AirHandler{}, false
	}
	if seen := a.seenAt(blowerCacheKey); !seen.IsZero() {
		ah.LastUpdated = &seen
	}
	return ah, true
}

// getHeatPumpRaw is the unstamped counterpart to GetHeatPump; see
// getAirHandlerRaw for why the distinction exists.
func (a *Api) getHeatPumpRaw() (HeatPump, bool) {
	h := a.Cache.Get(heatpumpCacheKey)
	th, ok := h.(*HeatPump)
	if !ok {
		return HeatPump{}, false
	}
	return *th, true
}

func (a *Api) GetHeatPump() (HeatPump, bool) {
	hp, ok := a.getHeatPumpRaw()
	if !ok {
		return HeatPump{}, false
	}
	hp.TempUnit = a.TempUnit()
	if seen := a.seenAt(heatpumpCacheKey); !seen.IsZero() {
		hp.LastUpdated = &seen
	}
	return hp, true
}

func (a *Api) GetTableRaw(deviceAddr uint16, table []byte) []byte {
	var addr TableAddr
	copy(addr[:], table[0:3])
	raw := rawRequest{Data: &[]byte{}}

	if a.Bus.Read(uint16(deviceAddr), addr, raw) {
		return *raw.Data
	}
	return nil
}

func (a *Api) UpdateThermostat(table Table, flags uint8) bool {
	return a.Bus.WriteTable(DevTSTAT, table, flags)
}

func (a *Api) NewListener() *dispatcher.Listener {
	return a.dispatcher.NewListener()
}
