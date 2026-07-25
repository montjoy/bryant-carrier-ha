# Bryant/Carrier HVAC

Controls a Carrier Infinity or Bryant Evolution HVAC system by impersonating a
SAM on the ABCD (RS-485) bus, and publishes the thermostat to Home Assistant
over MQTT discovery — no custom component required.

Built on the [Infinitive](https://github.com/acd/infinitive) daemon, which is
compiled from source bundled in this repository.

## Before you start

You need a USB RS-485 adapter wired to the **A** and **B** terminals of the ABCD
bus connector in your furnace.

> **Do not connect to the C and D terminals.** Those carry 24V AC and will
> destroy the adapter.

FTDI-chipset adapters are the most reliable. Solid-core thermostat wire is
preferred; several people have reported flaky communication with stranded core.

## Setup

1. Install the **Mosquitto broker** add-on and set up the **MQTT** integration
   if you have not already. This add-on picks up the broker credentials
   automatically — there is nothing to configure for the normal case.
2. Plug in the RS-485 adapter, then find its stable path under
   **Settings → System → Hardware → All Hardware**. Look for an entry under
   `/dev/serial/by-id/`.
3. Set the `serial` option to that path and start the add-on.
4. Within a few seconds the log should show frames being read. The thermostat
   appears in Home Assistant as a device named by the `device_name` option, with
   a climate entity plus sensors.

## Options

| Option | Default | Notes |
|---|---|---|
| `serial` | `/dev/ttyUSB0` | Path to the RS-485 adapter. **Use a `/dev/serial/by-id/...` path** — `/dev/ttyUSB0` is assigned in probe order and can change across a reboot or replug. |
| `log_level` | `info` | `trace`, `debug`, `info`, `warn`, `error`. `debug` logs every bus frame, which is a lot. |
| `zone` | `1` | Which zone to expose. Single-zone systems are always zone 1. |
| `min_setpoint` | `45` | Lowest temperature Home Assistant will offer. |
| `max_setpoint` | `95` | Highest temperature Home Assistant will offer. |
| `min_setpoint_spread` | `2` | Smallest gap kept between the heat and cool setpoints. Setting one close to the other pushes the other out of the way rather than letting the equipment silently override the request. |
| `device_name` | `Bryant/Carrier HVAC` | Device name in Home Assistant. Purely cosmetic — safe to change at any time. |
| `mqtt_enabled` | `true` | Turn off to run the web UI and JSON API only. |
| `discovery_prefix` | `homeassistant` | Only change this if you changed it in the MQTT integration. |
| `topic_prefix` | `bryant_carrier` | Root of this add-on's own state and command topics. |
| `node_id` | `bryant_carrier` | Namespaces the entities and forms the device identifier — you get `climate.bryant_carrier_thermostat`. **Changing this creates a new device** and orphans the old one, so pick it before you first start the add-on. |
| `mqtt_host` | *(unset)* | Only needed for a broker Home Assistant does not know about. |
| `mqtt_port` | `1883` | |
| `mqtt_username` | *(unset)* | |
| `mqtt_password` | *(unset)* | |

## What shows up in Home Assistant

A single device with:

- **Climate** — modes off / heat / cool / heat-cool, fan auto / low / medium /
  high, a `hold` preset, current temperature and humidity, and the current
  action (heating, cooling, fan, idle, off).
- **Sensors** — outdoor temperature, target humidity, hold duration, stage,
  blower RPM, airflow, heat pump coil temperature, heat pump outside
  temperature, heat pump stage.
- **Binary sensor** — electric heat.

In heat or cool mode the climate entity shows a single target temperature; in
heat-cool it shows a range. That mirrors how the equipment actually works — both
setpoints exist at all times, but only one is in play outside of auto.

The heat pump and air handler readings are gathered by passively watching bus
traffic, and nothing on the bus announces that a device has gone away. If one
stops being heard from for five minutes its entities go **unavailable** rather
than freezing on a stale value.

## Web UI

The add-on's ingress panel serves the original Infinitive interface, and the
JSON API is available on port 8080 if you want to script against it directly.
See the repository README for the endpoints.

## Troubleshooting

**The add-on stops immediately with "Serial device ... does not exist".**
The path in the `serial` option is wrong or the adapter is unplugged. Check
Settings → System → Hardware.

**No frames in the log.** A and B are probably swapped — harmless, just swap
them back. Also confirm you are on A/B and not C/D.

**The device never appears in Home Assistant.** Confirm the MQTT integration is
set up and the log shows `mqtt connected, publishing discovery`. If
`discovery_prefix` has been changed in the MQTT integration, it must match here.

**Values look wrong for a second and then correct themselves.** Occasional
mis-parsed frames are a known quirk of the reverse-engineered protocol and are
harmless.

## Credits

Built on [Infinitive](https://github.com/acd/infinitive) by Andrew Danforth, and
on the Home Assistant integration work by
[@mww012](https://github.com/mww012/ha_customcomponents) and
[@gogades](https://github.com/gogades/hass-infinitive).

## Disclaimer

This software talks to a proprietary system using information obtained by
reverse engineering. No warranty is provided. Use at your own risk to your HVAC
system and to yourself.
