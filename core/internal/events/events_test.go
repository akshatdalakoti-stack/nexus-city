package events

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestControlValidate(t *testing.T) {
	neg := int64(-1)
	ok := int64(7)
	cases := []struct {
		c     Control
		valid bool
	}{
		{Control{Action: "pause"}, true},
		{Control{Action: "resume"}, true},
		{Control{Action: "speed", Speed: 10}, true},
		{Control{Action: "speed", Speed: 3}, false},
		{Control{Action: "speed"}, false},
		{Control{Action: "reset"}, true},
		{Control{Action: "reset", Seed: &ok}, true},
		{Control{Action: "reset", Seed: &neg}, false},
		{Control{Action: ""}, false},
	}
	for _, c := range cases {
		if err := c.c.Validate(); (err == nil) != c.valid {
			t.Errorf("%+v: valid=%v, err=%v", c.c, c.valid, err)
		}
	}
}

// The JSON field names are a contract with the frontend and Python services.
func TestTelemetryFieldNames(t *testing.T) {
	v := 12.5
	b, _ := json.Marshal(Telemetry{
		Roads:    []RoadObs{{ID: "R1"}},
		Stations: []StationObs{{ID: "AQ1", PM25: &v, Status: "ok"}, {ID: "AQ2", Status: "offline"}},
	})
	s := string(b)
	for _, key := range []string{`"sim_time"`, `"summary"`, `"flow_vph"`, `"congestion"`, `"pm_grid"`, `"pm25":12.5`, `"pm25":null`, `"avg_congestion"`} {
		if !strings.Contains(s, key) {
			t.Errorf("telemetry JSON missing %s", key)
		}
	}
}
