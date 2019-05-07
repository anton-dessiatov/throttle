package app

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
)

// ListenAt is a type for listening specifications compatible with net.Listen
// function.
type ListenAt string

// UnmarshalJSON is an implementation of json.Unmarshaler for ListenAt
func (x *ListenAt) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}

	*x = ListenAt(s)
	return nil
}

// Limit is a bandwidth limit expressed in bytes per second.
type Limit int64

// uom stands for Unit Of Measurement
var uomSuffixes = []struct {
	unit string
	mul  int64
}{
	{unit: "kbps", mul: 1024},
	{unit: "mbps", mul: 1024 * 1024},
	{unit: "gbps", mul: 1024 * 1024 * 1024},
	{unit: "bps", mul: 1},
}

// Tries to parse an UOM suffix from a string. Returns string stripped from that
// suffix and a multiplier. If no suffix matches, returns string as is and 1 as
// a multiplier.
func parseSuffix(s string) (string, int64) {
	for _, v := range uomSuffixes {
		if strings.HasSuffix(strings.ToLower(s), v.unit) {
			return s[0 : len(s)-len(v.unit)], v.mul
		}
	}

	return s, 1
}

// UnmarshalJSON is an implementation of json.Unmarshaler for Limit
func (x *Limit) UnmarshalJSON(data []byte) error {
	var bytesPerSecond int64
	err := json.Unmarshal(data, &bytesPerSecond)
	if err == nil {
		// We have successfully unmarshaled an int64. Assume it's bytes per second.
	} else {
		// Okay, we might be dealing with a suffix. Let's check.
		var s string
		err = json.Unmarshal(data, &s)
		if err != nil {
			// Whoa! Something is seriously odd.
			return err
		}

		numberString, mul := parseSuffix(s)
		bytesPerSecond, err = strconv.ParseInt(numberString, 10, 64)
		if err != nil {
			return err
		}
		bytesPerSecond *= mul
	}

	if bytesPerSecond <= 0 {
		return errors.New("%v is not a valid value for bandwidth limit")
	}

	*x = Limit(bytesPerSecond)
	return nil
}

type Configuration struct {
	Tunnels map[ListenAt]TunnelConfig `json:"tunnels"`
}

type TunnelConfig struct {
	Connect         string `json:"connect"`
	TunnelLimit     Limit  `json:"tunnelLimit"`
	ConnectionLimit Limit  `json:"connectionLimit"`
}

// LoadAndWatch loads configuration from a given path, pushes it onto a
// configUpdate channel and starts listening for SIGUSR2 signals to reload
// config until quit channel gets closed. Upon each SIGUSR2 configuration is
// reloaded and sent to configUpdate.
func LoadAndWatch(path string, configUpdate chan<- Configuration, gs *gracefulShutdown) error {
	initial, err := load(path)
	if err != nil {
		return err
	}

	configUpdate <- initial

	s := make(chan os.Signal, 1)
	signal.Notify(s, syscall.SIGUSR2)

	gs.waitGroup.Add(1)

	go func() {
		defer gs.waitGroup.Done()
		for {
			select {
			case <-s:
				updated, err := load(path)
				if err != nil {
					log.Printf("Failed to reload configuration from %q: %v\n", path, err)
				} else {
					configUpdate <- updated
				}
			case <-gs.quit:
				log.Printf("Stopped watching the file")
				return
			}
		}
	}()

	return nil
}

func load(path string) (Configuration, error) {
	contents, err := ioutil.ReadFile(path)
	if err != nil {
		log.Printf("Failed to read configuration file at %q: %v\n", path, err)
		return Configuration{}, err
	}

	temp := new(Configuration)

	if err = json.Unmarshal(contents, temp); err != nil {
		log.Printf("Failed to parse configuration file at %q: %v\n", path, err)
		return Configuration{}, err
	}

	return *temp, nil
}
