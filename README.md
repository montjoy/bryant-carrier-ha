# bryant-carrier-ha

Carrier Infinity / Bryant Evolution HVAC control for Home Assistant, as a single
Home Assistant add-on.

The add-on talks to your furnace over the ABCD (RS-485) bus and publishes the
thermostat to Home Assistant using MQTT discovery. **There is no custom
component to install** — the climate entity and its sensors appear on their own.

## Why this exists

Getting one of these systems into Home Assistant used to take three separately
maintained projects:

| Project | Role |
|---|---|
| [`acd/infinitive`](https://github.com/acd/infinitive) | Go daemon speaking the ABCD bus |
| [`gogades/hass-infinitive`](https://github.com/gogades/hass-infinitive) | Home Assistant custom component |
| `montjoy/ha_infinitive` | add-on wrapping the daemon |

That arrangement broke repeatedly, and structurally so: the component reached
for JSON fields that only existed in a *different* fork of the daemon than the
add-on built, it pulled an unmaintained `pyinfinitive` package off PyPI, and the
add-on compiled whatever happened to be on the daemon's default branch that day.
Nothing versioned together, so any one piece moving broke the whole chain.

This repository collapses all of it into one add-on that builds pinned source
and needs nothing on the Home Assistant side.

## Installation

1. **Settings → Add-ons → Add-on Store**, then ⋮ → **Repositories**, and add:

   ```
   https://github.com/montjoy/bryant-carrier-ha
   ```

2. Install **Infinitive** from the store.
3. Install the **Mosquitto broker** add-on and set up the **MQTT** integration
   if you have not already.
4. Set the `serial` option to your adapter's `/dev/serial/by-id/...` path and
   start it.

Full option reference and wiring notes are in [the add-on docs](infinitive/DOCS.md).

### Migrating from `montjoy/ha_infinitive`

Add-on identity is derived from the repository, so this installs alongside the
old one rather than upgrading it. Note your existing options, **uninstall the
old add-on**, then install this one. If you also had the `hass-infinitive`
custom component, delete `custom_components/infinitive` and remove the
`climate:` platform block from `configuration.yaml` — it is no longer used.

## Layout

```
repository.yaml           # marks this repo as an add-on repository
infinitive/               # the add-on
├─ config.yaml
├─ build.yaml
├─ Dockerfile             # multi-stage: golang builder -> HA base image
├─ DOCS.md
├─ rootfs/                # s6 service definition
└─ daemon/                # Go source, compiled at image build time
```

## What changed from upstream

The daemon here is `acd/infinitive` at `2eaa5d2` plus:

**Correctness**

- **Zone hold read the wrong bit.** `1<<zone-1` parses as `(1<<zone)-1` in Go,
  because `<<` binds tighter than `-`. Only zone 1 was correct, and the read
  path disagreed with the write path. Both now share one implementation with
  tests covering all eight zones.
- **`tempUnit` was always empty** — declared on two API structs and never
  assigned. It is now read from the thermostat settings table.
- **Unknown mode strings silently meant "off".** `StringModeToRaw` mapped
  anything unrecognized to 5, so a typo in an API request would shut the system
  down. Invalid values are now rejected.
- Unknown fan modes silently became `auto`; they are now rejected too.
- A zero-length serial read with no error would panic the reader goroutine on
  `err.Error()`.

**New data**

- `targetHumidity` and `holdDurationMins` are now exposed. Both were already
  sitting in the zone params table, unread.
- Passively snooped air handler and heat pump values carry a `lastUpdated`
  stamp. Nothing on the bus announces that a device has gone away, so without
  this a dead heat pump reported its last reading forever.

**Operability**

- Log level is a flag instead of hardcoded `debug`, and per-frame logging moved
  off the info level — it made the default log unusable.
- SIGTERM is handled: the MQTT entities are marked offline and HTTP drains,
  instead of the process being killed outright on add-on stop.
- The bundled web UI used absolute `/api/...` paths, which 404 behind Home
  Assistant's ingress proxy. It now derives its own base path, so the ingress
  panel works.

**Home Assistant**

- MQTT discovery for the climate entity, nine sensors and a binary sensor,
  replacing the custom component entirely.
- `electric` and `heatpump` — the read-only modes the thermostat reports when
  those sources are selected at the wall unit — map to **heat**. The old
  component let them fall through to **off**, so an actively running system
  displayed as Off.
- `hvac_action` reports **fan** when the blower is running on its own. The old
  component only ever produced heating, cooling, or idle.

## JSON API

The daemon's HTTP API remains available on port 8080 for scripting.

| Method | Path | Notes |
|---|---|---|
| GET | `/api/zone/:zone/config` | zone 1–8 |
| PUT | `/api/zone/:zone/config` | `mode`, `fanMode`, `hold`, `heatSetpoint`, `coolSetpoint` |
| GET | `/api/airhandler` | blower RPM, airflow, electric heat |
| GET | `/api/heatpump` | coil temp, outside temp, stage |
| GET | `/api/tstat/settings` | |
| GET/PUT | `/api/zone/1/vacation` | |
| GET | `/api/raw/:device/:table` | raw table read, for protocol work |
| GET | `/api/ws` | websocket push of state changes |

`mode` accepts `off`, `auto`, `heat`, `cool`. It may additionally *report*
`electric` or `heatpump` when those are selected at the thermostat.

## Development

```
cd infinitive/daemon
go test ./...
go build .
```

The add-on image is built from this source; there is no `go install` step and no
network fetch of the daemon at build time.

## Credits

[Infinitive](https://github.com/acd/infinitive) by Andrew Danforth did the hard
part — reverse engineering the protocol. The Home Assistant mapping draws on
work by [@mww012](https://github.com/mww012/ha_customcomponents) and
[@gogades](https://github.com/gogades/hass-infinitive).

## Disclaimer

This software interacts with a proprietary system using information gleaned by
reverse engineering. No guarantee or warranty is provided. Use at your own risk
to your HVAC system and yourself.
