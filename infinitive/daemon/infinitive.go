package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/montjoy/bryant-carrier-ha/infinitive/daemon/infinity"
	"github.com/montjoy/bryant-carrier-ha/infinitive/daemon/mqtt"
	log "github.com/sirupsen/logrus"
)

// version is stamped at build time with -ldflags "-X main.version=..." and
// reported to Home Assistant as the device's software version.
var version = "dev"

func main() {
	httpPort := flag.Int("httpport", 8080, "HTTP port to listen on")
	serialPort := flag.String("serial", "", "path to serial port")
	logLevel := flag.String("loglevel", "info", "log level: trace, debug, info, warn, error")

	mqttCfg := mqtt.DefaultConfig()
	flag.StringVar(&mqttCfg.Broker, "mqtt-broker", "",
		"MQTT broker as host:port; enables Home Assistant MQTT discovery when set")
	flag.StringVar(&mqttCfg.Username, "mqtt-username", "", "MQTT username")
	flag.StringVar(&mqttCfg.Password, "mqtt-password", "", "MQTT password")
	flag.StringVar(&mqttCfg.ClientID, "mqtt-client-id", mqttCfg.ClientID, "MQTT client id")
	flag.StringVar(&mqttCfg.DiscoveryPrefix, "mqtt-discovery-prefix", mqttCfg.DiscoveryPrefix,
		"Home Assistant MQTT discovery prefix")
	flag.StringVar(&mqttCfg.TopicPrefix, "mqtt-topic-prefix", mqttCfg.TopicPrefix,
		"root topic for state and command messages")
	flag.StringVar(&mqttCfg.NodeID, "mqtt-node-id", mqttCfg.NodeID,
		"namespace for this daemon's entities; also the Home Assistant device identifier")
	flag.StringVar(&mqttCfg.DeviceName, "mqtt-device-name", mqttCfg.DeviceName,
		"device name shown in Home Assistant")
	flag.IntVar(&mqttCfg.Zone, "zone", mqttCfg.Zone, "zone to expose over MQTT")
	minSetpoint := flag.Uint("min-setpoint", uint(mqttCfg.MinSetpoint), "lowest settable temperature")
	maxSetpoint := flag.Uint("max-setpoint", uint(mqttCfg.MaxSetpoint), "highest settable temperature")
	minSpread := flag.Uint("min-setpoint-spread", uint(mqttCfg.MinSetpointSpread),
		"smallest allowed gap between the heat and cool setpoints")

	flag.Parse()

	if len(*serialPort) == 0 {
		fmt.Print("must provide serial\n")
		flag.PrintDefaults()
		os.Exit(1)
	}

	level, err := log.ParseLevel(*logLevel)
	if err != nil {
		fmt.Printf("invalid loglevel %q: %s\n", *logLevel, err)
		flag.PrintDefaults()
		os.Exit(1)
	}
	log.SetLevel(level)

	if mqttCfg.Zone < 1 || mqttCfg.Zone > 8 {
		fmt.Printf("zone must be between 1 and 8, got %d\n", mqttCfg.Zone)
		os.Exit(1)
	}
	if *minSetpoint >= *maxSetpoint {
		fmt.Printf("min-setpoint (%d) must be below max-setpoint (%d)\n", *minSetpoint, *maxSetpoint)
		os.Exit(1)
	}
	mqttCfg.MinSetpoint = uint8(*minSetpoint)
	mqttCfg.MaxSetpoint = uint8(*maxSetpoint)
	mqttCfg.MinSetpointSpread = uint8(*minSpread)
	mqttCfg.Version = version

	// Cancel on SIGINT/SIGTERM so the bus poller stops, the MQTT entities are
	// marked offline, and the webserver drains rather than the process being
	// killed outright.  s6 in the Home Assistant add-on sends SIGTERM on stop.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	infinityApi, err := infinity.NewApi(ctx, *serialPort)
	if err != nil {
		log.Panicf("error opening serial port: %s", err.Error())
	}

	if mqttCfg.Broker != "" {
		go mqtt.New(mqttCfg, infinityApi).Run(ctx)
	} else {
		log.Info("no mqtt broker configured, Home Assistant discovery disabled")
	}

	if err := launchWebserver(ctx, *httpPort, infinityApi); err != nil {
		log.Fatalf("webserver error: %s", err.Error())
	}
}
