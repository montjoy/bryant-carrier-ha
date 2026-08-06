# ABCD bus protocol notes

Everything here is reverse-engineered. Carrier publishes nothing, so treat this
as a record of what has been observed rather than a specification.

Each item is marked with how much weight it carries:

- **Observed** — seen on a real system, with the capture that shows it.
- **Working** — the code depends on it and the system behaves, but nothing has
  deliberately tested the boundaries.
- **Assumed** — inherited from [`acd/infinitive`](https://github.com/acd/infinitive)
  and never verified here. One of these was wrong; see [Mode](#mode-byte).

Captures below are from a Bryant dual-fuel system: a variable speed furnace at
`4001` and a heat pump at `5001`, single zone, Fahrenheit.

## Physical layer

**Working.** RS-485 half duplex, **38400 baud, 8N1**, on the **A** and **B**
terminals of the ABCD bus (`bus.go:73-79`). C and D are power — do not use them.
A and B swapped is harmless, it just produces no valid frames.

## Frame format

**Working** (`frame.go:94-142`).

```
 0  1   2  3   4      5  6    7    8 .. n-2   n-1  n
+------+------+------+------+------+----------+-------+
| dst  | src  | len  | 0  0 |  op  |   data   | crc16 |
+------+------+------+------+------+----------+-------+
```

| Bytes | Field | Notes |
|---|---|---|
| 0-1 | `dst` | big endian device address |
| 2-3 | `src` | big endian device address |
| 4 | `len` | length of `data` only |
| 5-6 | — | always zero in captures; purpose unknown |
| 7 | `op` | see below |
| 8..n-2 | `data` | |
| last 2 | CRC-16 | poly `0x8005`, reflected, init `0x0000`, xorout `0x0000`, **little endian** on the wire, computed over every preceding byte (`frame.go:26-30`) |

A frame of all zeros is discarded before the CRC is checked — the idle line
produces them.

### Ops

**Working** (`frame.go:11-24`). Only the first four are exercised by this daemon.

| Value | Name | Meaning |
|---|---|---|
| `0x02` | `ACK02` | |
| `0x06` | `ACK06` | response carrying data |
| `0x0b` | `READ` | read table block |
| `0x0c` | `WRITE` | write table block |
| `0x10` | `CHGTBN` | change table name |
| `0x15` | `NACK` | error |
| `0x1e` | `ALARM` | |
| `0x22` | `OBJRD` | read object data |
| `0x62` | `RDVAR` | read variable |
| `0x63` | `FORCE` | write variable |
| `0x64` | `AUTO` | |
| `0x75` | `LIST` | read list |

### Device addresses

**Observed.** `2001` and `9201` are constants in the code (`bus.go:13-16`); the
equipment addresses are the ranges the snoop filters match (`api.go:121,139`).

| Address | Device |
|---|---|
| `2001` | Thermostat |
| `9201` | SAM / this daemon — the address we transmit as |
| `4000`-`42ff` | Air handler / furnace |
| `5000`-`51ff` | Heat pump / outdoor unit |

The daemon is a passive participant: it polls the thermostat and **snoops**
frames between the thermostat and the equipment, since the equipment does not
answer queries from `9201`.

## Reads and writes

**Working** (`bus.go:244-257`).

A read sends `op=READ` with a 3-byte table address as the entire payload. The
response is `ACK06` whose data is the 3-byte address, 3 more bytes, then the
table contents — the decoder skips 6 bytes and unmarshals the rest big endian
(`bus.go:228-239`).

A write sends `op=WRITE` with:

```
[3 bytes table address][0x00 0x00 flags][full table struct]
```

The **flags** byte selects which fields the thermostat actually applies. The
whole struct goes on the wire regardless, so unselected fields are usually sent
as zero and ignored. Response is a bare `ACK06 00`.

**An ACK does not mean the write did anything.** A well-formed frame carrying a
value the thermostat does not recognize is ACKed and discarded silently. That is
what made the off bug so hard to see — see [Mode](#mode-byte).

Response timeout is 200ms with retransmission (`bus.go:18`).

## Tables

### `00 3B 02` — thermostat current parameters

**Working**, 26 bytes (`tables.go:13-40`). Offsets are into the struct, after
the 6 bytes the decoder skips.

| Offset | Size | Field |
|---|---|---|
| 0-7 | 8 | Zone 1-8 current temperature, °F, `0xff` = zone absent |
| 8-15 | 8 | Zone 1-8 current humidity, % |
| 16 | 1 | unknown |
| 17 | 1 | outdoor air temperature, **signed** |
| 18 | 1 | zone unoccupied bitflags |
| **19** | 1 | **mode** — see below |
| 20-24 | 5 | unknown |
| 25 | 1 | displayed zone |

Write flag `0x10` selects the mode byte. **Observed** — that is the flag used by
the write in the capture below, which the thermostat acted on once the value was
right.

#### Mode byte

Byte 19 packs two things (`api.go:235-236`):

```
 7  6  5   4   3  2  1  0
+--------+-----+---------+
| stage  |  ?  |  mode   |
+--------+-----+---------+
   >>5    unread   &0xf
```

Bit 4 is read by nothing. No capture has shown it set.

| Value | Meaning | Confidence |
|---|---|---|
| 0 | heat | Working |
| 1 | cool | **Observed** |
| 2 | auto | Working |
| 3 | electric heat only | **Assumed** — never seen; on a gas system it may mean something else entirely |
| 4 | **off** | **Observed** |
| 5 | — | **Assumed** off by upstream; never seen. Read as off here for lack of anything better |

**Upstream had this wrong**, and it cost an evening. `acd/infinitive` reads 4 as
"heat pump only" and writes 5 for off. On this system:

- Writing 5 produces a frame the thermostat ACKs and ignores. No error anywhere,
  the system keeps running, and the card snaps back on the next poll.
- The real off state, 4, was read as "heat pump only", which maps to Home
  Assistant's `heat`, so an off system displayed as heating.

The capture, pressing Off at the wall unit while in cool:

```
before  003b02 010000 52ffffffffffffff 2f2f2f2f2f2f2f2f 00 59 00 [01] 0000040439 01
after   003b02 010000 52ffffffffffffff 2f2f2f2f2f2f2f2f 00 59 00 [04] 0000040439 01
                      └ zone temps ──┘ └── humidity ──┘  ↑  ↑  ↑   ↑
                                                          │  │  │   └ mode 0x01 -> 0x04
                                                          │  │  └ zone unocc
                                                          │  └ outdoor 0x59 = 89°F
                                                          └ unknown
```

Only byte 19 changed. Immediately afterwards the thermostat shut the furnace
down, which is what rules out "heat pump only" on a system that does have a heat
pump:

```
2001 -> 4001: WRITE 000305 000000000000000000000000
2001 -> 4001: WRITE 000307 000000
```

The failed write of 5, for comparison — well-formed, ACKed, ignored:

```
9201 -> 2001: WRITE 003b02 000010 0000000000000000 0000000000000000 00 00 00 05 0000000000 00
                    addr   flags                                              ↑
2001 -> 9201: ACK06 00                                              mode 0x05, meaningless
```

### `00 3B 03` — thermostat zone parameters

**Working**, 178 bytes (`tables.go:42-98`).

| Offset | Size | Field |
|---|---|---|
| 0-7 | 8 | Zone 1-8 fan mode |
| 8 | 1 | zone hold bitflags, **zone 1 in the low bit** (`zonehold.go`) |
| 9-16 | 8 | Zone 1-8 heat setpoint |
| 17-24 | 8 | Zone 1-8 cool setpoint |
| 25-32 | 8 | Zone 1-8 target humidity |
| 33 | 1 | fan auto config |
| 34 | 1 | unknown |
| 35-50 | 16 | Zone 1-8 hold duration, uint16 each |
| 51-146 | 96 | Zone 1-8 name, 12 bytes each, space padded |

Write flags, **Working** (`api.go:269-297`):

| Flag | Field |
|---|---|
| `0x01` | fan mode |
| `0x02` | zone hold |
| `0x04` | heat setpoint |
| `0x08` | cool setpoint |

Hold is read-modify-write: the whole bitfield is on the wire, so the current
value must be read first or setting one zone clears the others.

### `00 3B 04` — vacation parameters

**Assumed** (`tables.go:134-146`). Never exercised in this session.

| Offset | Size | Field | Write flag |
|---|---|---|---|
| 0 | 1 | active | — |
| 1-2 | 2 | hours, uint16 | `0x02` |
| 3 | 1 | min temperature | `0x04` |
| 4 | 1 | max temperature | `0x08` |
| 5 | 1 | min humidity | `0x10` |
| 6 | 1 | max humidity | `0x20` |
| 7 | 1 | fan mode | `0x40` |

Note the API takes **days** and multiplies by 24 to get hours on write, but
divides by **7** on read (`tables.go:167,180`). Those disagree; one is a bug.
Untested either way.

### `00 3B 06` — thermostat settings

**Working** for `TempUnits`, which is the only field read (`api.go:114`). The
rest is **Assumed** (`tables.go:217-233`).

| Offset | Size | Field |
|---|---|---|
| 0 | 1 | backlight setting |
| 1 | 1 | **auto mode** — whether auto changeover is enabled |
| 2 | 1 | unknown |
| 3 | 1 | deadband |
| 4 | 1 | cycles per hour |
| 5 | 1 | schedule periods |
| 6 | 1 | programs enabled |
| 7 | 1 | temperature units — 0 = Fahrenheit, 1 = Celsius |
| 8 | 1 | unknown |
| 9-28 | 20 | dealer name |
| 29-48 | 20 | dealer phone |

Byte 1 is worth a look: it may say whether the system supports auto changeover,
which is currently the manual `auto_mode` add-on option. Nothing reads it today.

## Snooped equipment tables

The daemon never requests these. It watches the thermostat poll the equipment
and reads the replies (`api.go:119-153`).

### Air handler, `4000`-`42ff`

**Working.** Offsets are into the payload after the 3-byte table address.

| Table | Offset | Field |
|---|---|---|
| `00 03 06` | 1-2 | blower RPM, uint16 |
| `00 03 16` | 0 | electric heat active if `& 0x03` |
| `00 03 16` | 4-5 | airflow CFM, uint16 |

Also seen but not decoded: `00 03 02`, `00 03 05`, `00 03 07`, `00 03 0a`,
`00 03 0d`, `00 01 04` (model and serial as ASCII — `VARIABLE SPEED FURNACE`),
`00 04 02`, `00 04 03`.

### Heat pump, `5000`-`51ff`

**Working.**

| Table | Offset | Field |
|---|---|---|
| `00 3E 01` | 0-1 | outside temperature, uint16, **÷16** |
| `00 3E 01` | 2-3 | coil temperature, uint16, **÷16** |
| `00 3E 02` | 0 | stage, `>> 1` |

Also seen but not decoded: `00 3E 04`, `00 05 04`.

## Other encodings

### Fan mode

**Working** (`conversions.go:43-71`). Same values in `TStatZoneParams` and
`TStatVacationParams`.

| Value | Meaning |
|---|---|
| 0 | auto |
| 1 | low |
| 2 | med |
| 3 | high |

### Temperature units

**Working** for 0, **Assumed** for 1 — only checked against a Fahrenheit
thermostat (`conversions.go:73-87`).

| Value | Meaning |
|---|---|
| 0 | fahrenheit |
| 1 | celsius |

### Zone hold

**Working**, with a test over all eight zones (`zonehold.go`, `zonehold_test.go`).
One bit per zone, zone 1 in the low bit: `1 << (zone - 1)`.

Worth stating because it was wrong: the original expression was `1<<zone-1`,
which Go parses as `(1<<zone)-1` since `<<` binds tighter than `-`. That is a
correct mask only for zone 1.

## Open questions

- Mode value 3, and whether a gas system reports something for furnace-only or
  heat-pump-only operation. Capturing `003b02` byte 19 in those modes would
  settle it, the same way byte 19 settled off.
- Mode byte bit 4. Never seen set.
- Bytes 5 and 6 of every frame header. Always zero.
- `00 3B 02` bytes 16 and 20-24.
- Vacation hours: `days * 24` on write versus `hours / 7` on read.
- `00 3B 06` byte 1, which may make the `auto_mode` option unnecessary.
