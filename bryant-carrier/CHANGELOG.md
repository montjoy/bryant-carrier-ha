# Changelog

## 0.1.5

- Selecting Off did nothing, and the wall unit's own Off state displayed as
  Heat. The thermostat encodes off as `4`, not the `5` inherited from upstream
  `acd/infinitive`, which read `4` as "heat pump only" instead. A write of `5`
  is a well-formed frame, so the thermostat ACKed it and then ignored it, since
  no mode matches — the system kept running with no error anywhere.

  Captured on a Bryant dual-fuel system: pressing Off at the wall while in cool
  moved byte 19 of table `003b02` from `0x01` to `0x04`, and the thermostat
  immediately shut the furnace down over the bus.

  Off now reads and writes `4`. `5` still reads as off, since upstream believed
  it was and no system has been seen reporting it.

## 0.1.4

- Test the configured serial device with `-c` instead of
  `bashio::fs.device_exists`. The helper does not test for a character device,
  so it rejected every serial port this add-on exists to open: the add-on
  refused to start on a `/dev/ttyUSB0` that `ls` reported in the same breath as
  `crw-rw---- 188, 0`. This is the cause of the "Serial device ... does not
  exist" failures in 0.1.0 through 0.1.3, and no configuration change worked
  around it.
- Drop the `devices:` entry added in 0.1.3 and the by-id symlink resolution
  along with it. Both addressed a mapping problem that turned out not to exist
  — `uart: true` had been mapping every device correctly all along — and the
  `devices:` path hardcoded one adapter's serial number, which would not exist
  on anyone else's host.

## 0.1.3

- Map the adapter explicitly with `devices:` so the Supervisor passes it with
  `--device`, which creates a real character device at the configured path.
  The path names one adapter's serial number and has to be edited per install.
- Follow a `/dev/serial/by-id/...` symlink down to the node it names when the
  link itself is not a character device. The Supervisor does not always map a
  symlink's target alongside it, and a link that resolves to nothing fails to
  open exactly as if the adapter were absent. This keeps the stable by-id path
  in the option, which survives a replug where `/dev/ttyUSB0` does not.

## 0.1.2

- List serial devices with `ls -ld` rather than by name when the configured
  device is missing, so a character device and a symlink pointing at something
  that was never mapped into the container are told apart on sight.
- Echo the configured value through `printf %q`, so a stray character shows up
  instead of printing as an ordinary-looking path.
- Trim surrounding whitespace from the `serial` option. A pasted by-id path
  picks it up easily, and it failed every filesystem test on a device that was
  really present.
- Stop logging `No such file or directory` on every service exit. The shutdown
  script read `/run/s6-linux-init-container-results/exit` unconditionally, but
  the file exists only while s6-overlay is acting as container init, so the
  error printed on top of whatever was actually being reported.

## 0.1.1

- List the serial devices actually present in the container when the configured
  one is missing. The previous message restated the path already configured,
  which left a wrong path and a device that was never mapped into the container
  indistinguishable without a `docker exec` that add-ons cannot perform.

## 0.1.0

Initial release, combining `infinitive`, `hass-infinitive` and `ha_infinitive`
into a single add-on that builds pinned daemon source and publishes Home
Assistant MQTT discovery from the daemon itself, so no custom component is
required.

- Zone hold read the wrong bit: `1<<zone-1` parses as `(1<<zone)-1`, so only
  zone 1 was correct and the read path disagreed with the write path.
- `StringModeToRaw` mapped anything unrecognized to "off", so a typo in a
  request body could shut the system down. Invalid modes are now rejected.
- A zero-length serial read with a nil error panicked the reader goroutine.
- `electric` and `heatpump`, the read-only modes reported when those sources are
  selected at the wall unit, fell through to "off", so an actively running
  system displayed as Off. They map to heat.
- `hvac_action` reports `fan` when the blower runs on its own.
