package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"golang.org/x/net/websocket"

	"github.com/gin-gonic/gin"
	"github.com/montjoy/infinitive-ha/infinitive/daemon/infinity"
	"github.com/montjoy/infinitive-ha/infinitive/daemon/internal/assets"
	log "github.com/sirupsen/logrus"
)

func handleErrors(c *gin.Context) {
	c.Next()

	if len(c.Errors) > 0 {
		c.JSON(-1, c.Errors) // -1 == do not override the current error code
	}
}

type webserver struct {
	srv *http.Server
	api *infinity.Api
}

// shutdownGrace bounds how long we wait for in-flight requests to finish
// before forcing the listener closed.
const shutdownGrace = 5 * time.Second

// launchWebserver blocks until the server fails or ctx is cancelled, then
// drains in-flight requests.  Returning cleanly on cancellation is what lets
// the add-on stop without s6 having to kill the process.
func launchWebserver(ctx context.Context, port int, api *infinity.Api) error {
	ws := webserver{
		api: api,
	}
	ws.srv = &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: ws.buildEngine().Handler(),
	}

	errCh := make(chan error, 1)
	go func() {
		err := ws.srv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutting down webserver")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		return ws.srv.Shutdown(shutdownCtx)
	}
}

func (ws *webserver) buildEngine() *gin.Engine {
	r := gin.Default()
	r.Use(handleErrors) // attach error handling middleware

	api := r.Group("/api")

	api.GET("/tstat/settings", func(c *gin.Context) {
		tss, ok := ws.api.GetTstatSettings()
		if ok {
			c.JSON(200, tss)
		}
	})

	parseZone := func(c *gin.Context) (int, bool) {
		zone, err := strconv.Atoi(c.Param("zone"))
		if err != nil || zone < 1 || zone > 8 {
			c.AbortWithError(http.StatusBadRequest, fmt.Errorf("invalid zone: %s", c.Param("zone")))
			return 0, false
		}
		return zone, true
	}

	api.GET("/zone/:zone/config", func(c *gin.Context) {
		zone, ok := parseZone(c)
		if !ok {
			return
		}

		cfg, ok := ws.api.GetConfig(zone)
		if ok {
			c.JSON(200, cfg)
		}
	})

	getAirHandler := func(c *gin.Context) {
		ah, ok := ws.api.GetAirHandler()
		if ok {
			c.JSON(200, ah)
		}
	}

	getHeatPump := func(c *gin.Context) {
		hp, ok := ws.api.GetHeatPump()
		if ok {
			c.JSON(200, hp)
		}
	}

	api.GET("/airhandler", getAirHandler)
	api.GET("/heatpump", getHeatPump)
	// The routes below are for backward compatibility
	api.GET("/zone/1/airhandler", getAirHandler)
	api.GET("/zone/1/heatpump", getHeatPump)

	api.GET("/zone/1/vacation", func(c *gin.Context) {
		vac := infinity.TStatVacationParams{}
		ok := ws.api.Bus.ReadTable(infinity.DevTSTAT, &vac)
		if ok {
			c.JSON(200, vac.ToAPI())
		}
	})

	api.PUT("/zone/1/vacation", func(c *gin.Context) {
		var args infinity.APIVacationConfig

		if c.Bind(&args) != nil {
			log.Printf("bind failed")
			return
		}

		params := infinity.TStatVacationParams{}
		flags := params.FromAPI(&args)

		ws.api.UpdateThermostat(params, flags)
	})

	api.PUT("/zone/:zone/config", func(c *gin.Context) {
		zone, ok := parseZone(c)
		if !ok {
			return
		}

		var args infinity.TStatZoneConfig
		if c.Bind(&args) != nil {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}

		// Translate the wire format into a partial update.  A zero setpoint
		// means "not supplied" here, which is the long-standing behaviour of
		// this endpoint -- the setpoints are uint8 with no sentinel.
		update := infinity.ZoneUpdate{Hold: args.Hold}
		if len(args.FanMode) > 0 {
			update.FanMode = &args.FanMode
		}
		if len(args.Mode) > 0 {
			update.Mode = &args.Mode
		}
		if args.HeatSetpoint > 0 {
			update.HeatSetpoint = &args.HeatSetpoint
		}
		if args.CoolSetpoint > 0 {
			update.CoolSetpoint = &args.CoolSetpoint
		}

		if err := ws.api.SetZoneConfig(zone, update); err != nil {
			c.AbortWithError(http.StatusBadRequest, err)
			return
		}
	})

	api.GET("/raw/:device/:table", func(c *gin.Context) {
		matched, _ := regexp.MatchString("^[a-f0-9]{4}$", c.Param("device"))
		if !matched {
			c.AbortWithError(400, errors.New("name must be a 4 character hex string"))
			return
		}
		matched, _ = regexp.MatchString("^[a-f0-9]{6}$", c.Param("table"))
		if !matched {
			c.AbortWithError(400, errors.New("table must be a 6 character hex string"))
			return
		}

		d, _ := strconv.ParseUint(c.Param("device"), 16, 16)
		a, _ := hex.DecodeString(c.Param("table"))

		if response := ws.api.GetTableRaw(uint16(d), a); response != nil {
			c.JSON(200, gin.H{"response": hex.EncodeToString(response)})
		} else {
			c.AbortWithError(504, errors.New("timed out waiting for response"))
		}
	})

	api.GET("/ws", func(c *gin.Context) {
		h := websocket.Handler(ws.websocketListener)
		h.ServeHTTP(c.Writer, c.Request)
	})

	r.StaticFS("/ui", http.FS(assets.Assets))

	// Relative target with a trailing slash: relative so it survives the ingress
	// path prefix, trailing slash so the page's own relative asset and API URLs
	// resolve against /ui/ rather than /.  302 rather than 301 so a stale
	// redirect can't get pinned in a browser cache.
	r.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusFound, "ui/")
	})

	return r
}

func serializeUpdate(source string, data any) []byte {
	msg, _ := json.Marshal(&struct {
		Source string      `json:"source"`
		Data   interface{} `json:"data"`
	}{source, data})
	return msg
}

func (ws *webserver) websocketListener(wsConn *websocket.Conn) {
	listener := ws.api.NewListener()

	defer func() {
		listener.Close()
		log.Infof("%s: closing websocket", wsConn.RemoteAddr())
		if err := wsConn.Close(); err != nil {
			log.Errorf("%s: error on closing wsConn: %v", wsConn.RemoteAddr(), err)
		}
	}()

	for source, data := range ws.api.Cache.Dump() {
		wsConn.Write(serializeUpdate(source, data))
	}

	// wait for events
	for message := range listener.Receive() {
		if _, err := wsConn.Write(serializeUpdate(message.Source, message.Data)); err != nil {
			log.Infof("%s: error writing to wsConn: %v", wsConn.RemoteAddr(), err)
			return
		}
	}
}
