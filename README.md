# sdrpp-scheduler

Automated scheduled recordings for [SDR++](https://www.sdrpp.org/). Connects to SDR++ over its built-in rigctl TCP interface to tune to a frequency, set the demodulation mode, and start/stop the recorder — all driven by a cron schedule in a YAML config file.

```
2026/04/02 09:29:58 sdrpp-scheduler v1.0.0 starting
2026/04/02 09:29:58 Loaded 3 schedule entries from config.yaml
2026/04/02 09:29:58 Connected to SDR++ at localhost:4532
2026/04/02 09:30:00 === Starting job: NOAA-15 Pass ===
2026/04/02 09:30:00   Frequency : 137.620 MHz (137620000 Hz)
2026/04/02 09:30:00   Mode      : WFM (passband: 0 Hz)
2026/04/02 09:30:00   Duration  : 15m0s
2026/04/02 09:30:00   > Recording STARTED for "NOAA-15 Pass"
2026/04/02 09:45:00   . Duration elapsed for "NOAA-15 Pass"
2026/04/02 09:45:00   . Recording STOPPED for "NOAA-15 Pass"
2026/04/02 09:45:00 === Job complete: NOAA-15 Pass ===
```

---

## How it works

SDR++ includes a **RigCtl Server** module that speaks the [Hamlib rigctld](https://hamlib.github.io/) protocol over TCP (port 4532 by default). This program acts as a client, sending four types of commands at the scheduled time:

 | Command       | What SDR++ does                |
 | ---           | ---                            |
 | `F 137620000` | Tune the VFO to 137.620 MHz    |
 | `M WFM 0`     | Switch to Wide FM demodulation |
 | \start        | Start "playing"                |
 | `AOS`         | Start the Recorder module      |
 | `LOS`         | Stop the Recorder module       |

The scheduler wakes every 10 seconds, checks whether any configured job is due in the current minute, and dispatches it exactly once. Jobs are never overlapped — if a recording is still running when a new one is due, the new one is skipped with a warning.

---

## Requirements

- **Go 1.19** or newer
- **SDR++** running on the same machine with the RigCtl Server module enabled (see [SDR++ setup](#sdr-setup) below)

---

## SDR++ setup

This is a one-time configuration inside SDR++. You need to do this before running sdrpp-scheduler.

**1. Add a Recorder module**

Open SDR++ → Module Manager → type `Recorder` in the search box → click **Add**. A Recorder panel will appear in the left sidebar. Configure the output directory and file format there.

**2. Add and start the RigCtl Server module**

In Module Manager, search for `Rigctl Server` → click **Add**. A RigCtl Server panel appears. Click **Start** to begin listening.

**3. Configure the RigCtl Server panel**

- **Host**: `127.0.0.1`
- **Port**: `4532`
- Check **Tuning** ✓ — allows the scheduler to change frequency
- Check **Recording** ✓ — allows the scheduler to start/stop the Recorder
- **Controlled Recorder**: select the Recorder module you added in step 1

> The `AOS` (start recording) and `LOS` (stop recording) commands only work when **Recording** is checked and a recorder module is selected in this panel.

---

## Installation

```bash
git clone <this repo>
cd sdrpp-scheduler
go mod tidy        # downloads gopkg.in/yaml.v3
go build -o sdrpp-scheduler .
```

To install system-wide:

```bash
sudo cp sdrpp-scheduler /usr/local/bin/
```

---

## Usage

```
./sdrpp-scheduler [flags]

Flags:
  -config string   Path to config file (default: config.yaml)
  -dry-run         Preview the schedule without connecting to SDR++
  -verbose         Show all rigctl commands and responses
  -version         Print version and exit
```

**Preview your schedule before running:**

```bash
./sdrpp-scheduler -dry-run

=== Dry Run Mode - No connection to SDR++ ===
SDR++ server: localhost:4532

Name                           Frequency        Mode     Duration     Cron       Next Run
-----------------------------------------------------------------------------------------------------------
NOAA-15 Pass                   137.620 MHz      WFM      15m0s        30 9 * * * 2026-04-02 09:30
NOAA-18 Pass                   137.913 MHz      WFM      12m0s        0 14 * * * 2026-04-02 14:00
40m SSB Net                    7.200 MHz        USB      1h0m0s       0 20 * * 0 2026-04-06 20:00
```

**Normal run:**

```bash
./sdrpp-scheduler
./sdrpp-scheduler -config /etc/sdrpp/schedule.yaml
./sdrpp-scheduler -verbose    # see every rigctl command sent
```

Stop with **Ctrl+C**. Any recording in progress will finish before the program exits.

---

## Configuration

The config file is YAML. Copy `config.yaml` as a starting point.

### Full example

```yaml
sdrpp:
  host: "localhost"   # SDR++ machine hostname or IP
  port: 4532          # RigCtl Server port (must match SDR++ setting)
  timeout: 5s         # TCP connection timeout per command

schedule:

  - name: "NOAA-15 Pass"
    frequency_hz: 137620000   # Frequency in Hz — 137.620 MHz
    mode: "WFM"               # Demodulation mode
    passband: 0               # Filter bandwidth in Hz; 0 = SDR++ default
    duration: 15m             # How long to record
    cron: "30 9 * * *"        # When to start — daily at 09:30
    enabled: true

  - name: "40m SSB Net"
    frequency_hz: 7200000     # 7.200 MHz
    mode: "USB"
    passband: 2700            # 2.7 kHz SSB passband
    duration: 1h
    cron: "0 20 * * 0"        # Every Sunday at 20:00
    enabled: true

  - name: "Disabled example"
    frequency_hz: 145800000
    mode: "FM"
    duration: 10m
    cron: "0 10 * * 1,3,5"
    enabled: false            # Won't run until changed to true
```

### Schedule entry fields

 | Field          | Required | Description                                                                |
 | ---            | ---      | ---                                                                        |
 | `name`         | No       | Human-readable label shown in logs. Defaults to `entry-N`.                 |
 | `frequency_hz` | **Yes**  | Frequency to tune to, in Hz (e.g. `137620000` for 137.620 MHz)             |
 | `mode`         | No       | Demodulation mode. Defaults to `FM`. See modes below.                      |
 | `passband`     | No       | Filter bandwidth in Hz. `0` uses SDR++'s default for the mode.             |
 | `duration`     | **Yes**  | Recording length as a Go duration string: `5m`, `1h30m`, `90s`             |
 | `cron`         | **Yes**  | 5-field cron expression for when to start. See cron format below.          |
 | `enabled`      | No       | Set to `false` to skip this entry without deleting it. Defaults to `true`. |

### Supported modes

 | Mode         | Description                                              |
 | ---          | ---                                                      |
 | `WFM`        | Wide FM — broadcast radio, weather satellites (NOAA APT) |
 | `FM` / `NFM` | Narrow FM — voice comms, amateur radio VHF/UHF           |
 | `AM`         | Amplitude modulation — aviation, HF broadcast            |
 | `USB`        | Upper sideband — HF amateur, utility                     |
 | `LSB`        | Lower sideband — HF amateur below 10 MHz                 |
 | `CW`         | Continuous wave (Morse), upper sideband                  |
 | `CWR`        | CW, lower sideband                                       |
 | `RTTY`       | Radio teletype                                           |
 | `DSB`        | Double sideband                                          |
 | `RAW`        | Raw IQ passthrough                                       |

### Cron format

Standard 5-field cron: **`minute  hour  day-of-month  month  day-of-week`**

Day-of-week: `0` = Sunday, `1` = Monday … `6` = Saturday.

 | Expression       | Fires                                  |
 | ---              | ---                                    |
 | `30 9 * * *`     | Every day at 09:30                     |
 | `0 14 * * *`     | Every day at 14:00                     |
 | `0 20 * * 0`     | Every Sunday at 20:00                  |
 | `30 14 * * 1-5`  | Monday–Friday at 14:30                 |
 | `0 10 * * 1,3,5` | Monday, Wednesday, Friday at 10:00     |
 | `*/15 * * * *`   | Every 15 minutes                       |
 | `0 * * * *`      | Every hour on the hour                 |
 | `0 8 1 * *`      | 1st of every month at 08:00            |
 | `0 6 * 6,7,8 *`  | Every day in June/July/August at 06:00 |
 | `@hourly`        | Alias for `0 * * * *`                  |
 | `@daily`         | Alias for `0 0 * * *`                  |
 | `@weekly`        | Alias for `0 0 * * 0`                  |
 | `@monthly`       | Alias for `0 0 1 * *`                  |

---

## Running as a service

### Linux (systemd)

Create `/etc/systemd/system/sdrpp-scheduler.service`:

```ini
[Unit]
Description=SDR++ Recording Scheduler
# Start after SDR++ is already running. If SDR++ is also a service,
# replace the After line with: After=sdrpp.service
After=graphical-session.target

[Service]
ExecStart=/usr/local/bin/sdrpp-scheduler -config /etc/sdrpp/config.yaml
Restart=on-failure
RestartSec=15
User=YOUR_USERNAME
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=default.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now sdrpp-scheduler

# Watch the logs live
sudo journalctl -u sdrpp-scheduler -f
```

### Windows (Task Scheduler)

Create a basic task that runs at system startup:
- **Program**: `C:\path\to\sdrpp-scheduler.exe`
- **Arguments**: `-config C:\path\to\config.yaml`
- **Start in**: `C:\path\to\` (directory containing `config.yaml`)
- **Run whether user is logged on or not**: enabled

---

## Troubleshooting

**`Cannot reach SDR++ rigctl server`**

SDR++ is not running, or the RigCtl Server module isn't started. Open SDR++, go to the RigCtl Server panel, and click **Start**. Verify the host and port in your `config.yaml` match what's shown in the panel.

**Recording never starts or stops**

In the SDR++ RigCtl Server panel, confirm **Recording** is checked and a Recorder module is selected in the **Controlled Recorder** dropdown. The `AOS`/`LOS` commands have no effect without this.

**Mode doesn't change (`RPRT 1` in verbose output)**

Some SDR++ builds have partial mode support via rigctl. The frequency will still be set correctly — set the mode manually in SDR++ before running if needed.

**Job is skipped with "still recording" warning**

A previous job is still running when the next one is due. Either shorten the duration of the first job or stagger the cron times so they don't overlap.

**`invalid cron expression` on startup**

The cron field must have exactly 5 space-separated fields. Six-field cron (with a seconds field) is not supported. Check for extra whitespace or a stray field.

**`duration must be a positive duration string` on startup**

Duration values in `config.yaml` must be Go duration strings in quotes: `"15m"`, `"1h30m"`, `"90s"`. Bare numbers are not accepted.

---

## Architecture

```
sdrpp-scheduler/
├── main.go        CLI entry point, flag parsing, signal handling
├── config.go      YAML config loading, validation, Duration type
├── cron.go        5-field cron parser and next-fire-time calculator
├── rigctl.go      TCP rigctl client: SetFrequency, SetMode, AOS, LOS
├── scheduler.go   Main scheduling loop, job dispatch, concurrency control
├── config.yaml    Example/template configuration file
└── Makefile       Build, run, dry-run, install targets
```

The only external dependency is `gopkg.in/yaml.v3` for config parsing. The cron engine is implemented from scratch with no third-party libraries.

---

## License

MIT
