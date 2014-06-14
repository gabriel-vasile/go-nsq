package nsq

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"
)

// Config is a struct of NSQ options
//
// (see Config.Set() for available parameters)
type Config struct {
	sync.RWMutex
	initOnce sync.Once

	verbose bool `opt:"verbose"`

	readTimeout  time.Duration `opt:"read_timeout" min:"100ms" max:"5m"`
	writeTimeout time.Duration `opt:"write_timeout" min:"100ms" max:"5m"`

	lookupdPollInterval time.Duration `opt:"lookupd_poll_interval" min:"5s" max:"5m"`
	lookupdPollJitter   float64       `opt:"lookupd_poll_jitter" min:"0" max:"1"`

	maxRequeueDelay     time.Duration `opt:"max_requeue_delay" min:"0" max:"60m"`
	defaultRequeueDelay time.Duration `opt:"default_requeue_delay" min:"0" max:"60m"`
	backoffMultiplier   time.Duration `opt:"backoff_multiplier" min:"0" max:"60m"`

	maxAttempts       uint16        `opt:"max_attempts" min:"0" max:"65535"`
	lowRdyIdleTimeout time.Duration `opt:"low_rdy_idle_timeout" min:"1s" max:"5m"`

	clientID  string `opt:"client_id"`
	hostname  string `opt:"hostname"`
	userAgent string `opt:"user_agent"`

	heartbeatInterval time.Duration `opt:"heartbeat_interval"`
	sampleRate        int32         `opt:"sample_rate" min:"0" max:"99"`

	tlsV1     bool        `opt:"tls_v1"`
	tlsConfig *tls.Config `opt:"tls_config"`

	deflate      bool `opt:"deflate"`
	deflateLevel int  `opt:"deflate_level" min:"1" max:"9"`
	snappy       bool `opt:"snappy"`

	outputBufferSize    int64         `opt:"output_buffer_size"`
	outputBufferTimeout time.Duration `opt:"output_buffer_timeout"`

	maxInFlight      int `opt:"max_in_flight" min:"0"`
	maxInFlightMutex sync.RWMutex

	maxBackoffDuration time.Duration `opt:"max_backoff_duration" min:"0" max:"60m"`

	authSecret string `opt:"auth_secret"`
}

// NewConfig returns a new default configuration.
//
// 	"verbose":                false (bool)
// 	"read_timeout":           60s (min: 100ms, max: 5m) (time.Duration)
// 	"write_timeout":          1s (min: 100ms, max: 5m) (time.Duration)
// 	"lookupd_poll_interval":  60s (min: 5s, max: 5m) (time.Duration)
// 	"lookupd_poll_jitter":    0.3 (min: 0.0, max: 1.0) (float)
// 	"max_requeue_delay":      15m (min: 0, max: 60m) (time.Duration)
// 	"default_requeue_delay":  90s (min: 0, max: 60m) (time.Duration)
// 	"backoff_multiplier":     1s (min: 0, max: 60m) (time.TIme)
// 	"max_attempts":           5 (min: 0, max: 65535) (int)
// 	"low_rdy_idle_timeout":   10s (min: 1s, max: 5m) (time.Duration)
// 	"client_id":              "<short host name>" (string)
// 	"hostname":               os.Hostname() (string)
// 	"user_agent":             "go-nsq/<version>" (string)
// 	"heartbeat_interval":     30s (time.Duration)
// 	"sample_rate":            0 (min: 0, max: 99) (int)
// 	"tls_v1":                 false (bool)
// 	"tls_config":             nil (*tls.Config)
// 	"deflate":                false (bool)
// 	"deflate_level":          6 (min: 1, max: 9) (int)
// 	"snappy":                 false (bool)
// 	"output_buffer_size":     16384 (int)
// 	"output_buffer_timeout":  250ms (time.Duration)
// 	"max_in_flight":          1 (int)
// 	"max_backoff_duration":   120s (time.Duration)
// 	"auth_secret":            "" (string)
//
// See Config.Set() for a description of these parameters.
func NewConfig() *Config {
	conf := &Config{}
	conf.initialize()
	return conf
}

// initialize is used to ensure that a Config has a baseline set of defaults
// despite how it might have been insantiated
func (c *Config) initialize() {
	c.initOnce.Do(func() {
		hostname, err := os.Hostname()
		if err != nil {
			log.Fatalf("ERROR: unable to get hostname %s", err.Error())
		}
		c.maxInFlight = 1
		c.maxAttempts = 5
		c.lookupdPollInterval = 60 * time.Second
		c.lookupdPollJitter = 0.3
		c.lowRdyIdleTimeout = 10 * time.Second
		c.defaultRequeueDelay = 90 * time.Second
		c.maxRequeueDelay = 15 * time.Minute
		c.backoffMultiplier = time.Second
		c.maxBackoffDuration = 120 * time.Second
		c.readTimeout = DefaultClientTimeout
		c.writeTimeout = time.Second
		c.deflateLevel = 6
		c.outputBufferSize = 16 * 1024
		c.outputBufferTimeout = 250 * time.Millisecond
		c.heartbeatInterval = DefaultClientTimeout / 2
		c.clientID = strings.Split(hostname, ".")[0]
		c.hostname = hostname
		c.userAgent = fmt.Sprintf("go-nsq/%s", VERSION)
	})
}

// Set takes an option as a string and a value as an interface and
// attempts to set the appropriate configuration option.
//
// It attempts to coerce the value into the right format depending on the named
// option and the underlying type of the value passed in.
//
// Calls to Set() that take a time.Duration as an argument can be input as:
//
// 	"1000ms" (a string parsed by time.ParseDuration())
// 	1000 (an integer interpreted as milliseconds)
// 	1000*time.Millisecond (a literal time.Duration value)
//
// Calls to Set() that take bool can be input as:
//
// 	"true" (a string parsed by strconv.ParseBool())
// 	true (a boolean)
// 	1 (an int where 1 == true and 0 == false)
//
// It returns an error for an invalid option or value.
//
// 	verbose (bool): enable verbose logging
//
// 	read_timeout (time.Duration): the deadline set for network reads
// 	                              (min: 100ms, max: 5m)
//
// 	write_timeout (time.Duration): the deadline set for network writes
// 	                               (min: 100ms, max: 5m)
//
// 	lookupd_poll_interval (time.Duration): duration between polling lookupd for new
// 	                                       (min: 5s, max: 5m)
//
// 	lookupd_poll_jitter (float): fractional amount of jitter to add to the lookupd pool loop,
// 	                             this helps evenly distribute requests even if multiple
// 	                             consumers restart at the same time.
// 	                             (min: 0.0, max: 1.0)
//
// 	max_requeue_delay (time.Duration): the maximum duration when REQueueing
// 	                                   (for doubling of deferred requeue)
// 	                                   (min: 0, max: 60m)
//
// 	default_requeue_delay (time.Duration): the default duration when REQueueing
// 	                                       (min: 0, max: 60m)
//
// 	backoff_multiplier (time.Duration): the unit of time for calculating consumer backoff
// 	                                    (min: 0, max: 60m)
//
// 	max_attempts (int): maximum number of times this consumer will attempt to process a message
// 	                    (min: 0, max: 65535)
//
// 	low_rdy_idle_timeout (time.Duration): the amount of time in seconds to wait for a message
// 	                                      from a producer when in a state where RDY counts
// 	                                      are re-distributed (ie. max_in_flight < num_producers)
// 	                                      (min: 1s, max: 5m)
//
// 	client_id (string): an identifier sent to nsqd representing the client
//                      (defaults: short hostname)
//
// 	hostname (string): an identifier sent to nsqd representing the host
// 	                   (defaults: long hostname)
//
// 	user_agent (string): an identifier of the agent for this client (in the spirit of HTTP)
// 	                     (default: "<client_library_name>/<version>")
//
// 	heartbeat_interval (time.Duration): duration of time between heartbeats
//
// 	sample_rate (int): integer percentage to sample the channel (requires nsqd 0.2.25+)
// 	                   (min: 0, max: 99)
//
// 	tls_v1 (bool): negotiate TLS
//
// 	tls_config (*tls.Config): client TLS configuration
//
// 	deflate (bool): negotiate Deflate compression
//
// 	deflate_level (int): the compression level to negotiate for Deflate
// 	                     (min: 1, max: 9)
//
// 	snappy (bool): negotiate Snappy compression
//
// 	output_buffer_size (int): size of the buffer (in bytes) used by nsqd for
// 	                          buffering writes to this connection
//
// 	output_buffer_timeout (time.Duration): timeout (in ms) used by nsqd before flushing buffered
// 	                                       writes (set to 0 to disable).
// 	
// 	                                       WARNING: configuring clients with an extremely low
// 	                                       (< 25ms) output_buffer_timeout has a significant effect
// 	                                       on nsqd CPU usage (particularly with > 50 clients connected).
//
// 	max_in_flight (int): the maximum number of messages to allow in flight (concurrency knob)
//
// 	max_backoff_duration (time.Duration): the maximum amount of time to backoff when processing fails
// 	                                      0 == no backoff
//
// 	auth_secret (string): secret for nsqd authentication (requires nsqd 0.2.29+)
//
func (c *Config) Set(option string, value interface{}) error {
	c.Lock()
	defer c.Unlock()

	c.initialize()

	val := reflect.ValueOf(c).Elem()
	typ := val.Type()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		opt := field.Tag.Get("opt")
		min := field.Tag.Get("min")
		max := field.Tag.Get("max")

		if option != opt {
			continue
		}

		fieldVal := val.FieldByName(field.Name)
		dest := unsafeValueOf(fieldVal)
		coercedVal, err := coerce(value, field.Type)
		if err != nil {
			return fmt.Errorf("failed to coerce option %s (%v) - %s",
				option, value, err)
		}
		if min != "" {
			coercedMinVal, _ := coerce(min, field.Type)
			if valueCompare(coercedVal, coercedMinVal) == -1 {
				return fmt.Errorf("invalid %s ! %v < %v",
					option, coercedVal.Interface(), coercedMinVal.Interface())
			}
		}
		if max != "" {
			coercedMaxVal, _ := coerce(max, field.Type)
			if valueCompare(coercedVal, coercedMaxVal) == 1 {
				return fmt.Errorf("invalid %s ! %v > %v",
					option, coercedVal.Interface(), coercedMaxVal.Interface())
			}
		}
		dest.Set(coercedVal)
		return nil
	}

	return fmt.Errorf("invalid option %s", option)
}

// because Config contains private structs we can't use reflect.Value
// directly, instead we need to "unsafely" address the variable
func unsafeValueOf(val reflect.Value) reflect.Value {
	uptr := unsafe.Pointer(val.UnsafeAddr())
	return reflect.NewAt(val.Type(), uptr).Elem()
}

func valueCompare(v1 reflect.Value, v2 reflect.Value) int {
	switch v1.Type().String() {
	case "int", "int16", "int32", "int64":
		if v1.Int() > v2.Int() {
			return 1
		} else if v1.Int() < v2.Int() {
			return -1
		}
		return 0
	case "uint", "uint16", "uint32", "uint64":
		if v1.Uint() > v2.Uint() {
			return 1
		} else if v1.Uint() < v2.Uint() {
			return -1
		}
		return 0
	case "float32", "float64":
		if v1.Float() > v2.Float() {
			return 1
		} else if v1.Float() < v2.Float() {
			return -1
		}
		return 0
	case "time.Duration":
		if v1.Interface().(time.Duration) > v2.Interface().(time.Duration) {
			return 1
		} else if v1.Interface().(time.Duration) < v2.Interface().(time.Duration) {
			return -1
		}
		return 0
	}
	panic("impossible")
}

func coerce(v interface{}, typ reflect.Type) (reflect.Value, error) {
	var err error
	if typ.Kind() == reflect.Ptr {
		return reflect.ValueOf(v), nil
	}
	switch typ.String() {
	case "string":
		v, err = coerceString(v)
	case "int", "int16", "int32", "int64":
		v, err = coerceInt64(v)
	case "uint", "uint16", "uint32", "uint64":
		v, err = coerceUint64(v)
	case "float32", "float64":
		v, err = coerceFloat64(v)
	case "bool":
		v, err = coerceBool(v)
	case "time.Duration":
		v, err = coerceDuration(v)
	default:
		v = nil
		err = errors.New(fmt.Sprintf("invalid type %s", typ.String()))
	}
	return valueTypeCoerce(v, typ), err
}

func valueTypeCoerce(v interface{}, typ reflect.Type) reflect.Value {
	val := reflect.ValueOf(v)
	if reflect.TypeOf(v) == typ {
		return val
	}
	tval := reflect.New(typ).Elem()
	switch typ.String() {
	case "int", "int16", "int32", "int64":
		tval.SetInt(val.Int())
	case "uint", "uint16", "uint32", "uint64":
		tval.SetUint(val.Uint())
	case "float32", "float64":
		tval.SetFloat(val.Float())
	}
	return tval
}

func coerceString(v interface{}) (string, error) {
	switch v.(type) {
	case string:
		return v.(string), nil
	case int, int16, int32, int64, uint, uint16, uint32, uint64:
		return fmt.Sprintf("%d", v), nil
	case float64:
		return fmt.Sprintf("%f", v), nil
	default:
		return fmt.Sprintf("%s", v), nil
	}
	return "", errors.New("invalid value type")
}

func coerceDuration(v interface{}) (time.Duration, error) {
	switch v.(type) {
	case string:
		return time.ParseDuration(v.(string))
	case int, int16, int32, int64:
		// treat like ms
		return time.Duration(reflect.ValueOf(v).Int()) * time.Millisecond, nil
	case uint, uint16, uint32, uint64:
		// treat like ms
		return time.Duration(reflect.ValueOf(v).Uint()) * time.Millisecond, nil
	case time.Duration:
		return v.(time.Duration), nil
	}
	return 0, errors.New("invalid value type")
}

func coerceBool(v interface{}) (bool, error) {
	switch v.(type) {
	case bool:
		return v.(bool), nil
	case string:
		return strconv.ParseBool(v.(string))
	case int, int16, int32, int64:
		return reflect.ValueOf(v).Int() != 0, nil
	case uint, uint16, uint32, uint64:
		return reflect.ValueOf(v).Uint() != 0, nil
	}
	return false, errors.New("invalid value type")
}

func coerceFloat64(v interface{}) (float64, error) {
	switch v.(type) {
	case string:
		return strconv.ParseFloat(v.(string), 64)
	case int, int16, int32, int64:
		return float64(reflect.ValueOf(v).Int()), nil
	case uint, uint16, uint32, uint64:
		return float64(reflect.ValueOf(v).Uint()), nil
	case float64:
		return v.(float64), nil
	}
	return 0, errors.New("invalid value type")
}

func coerceInt64(v interface{}) (int64, error) {
	switch v.(type) {
	case string:
		return strconv.ParseInt(v.(string), 10, 64)
	case int, int16, int32, int64:
		return reflect.ValueOf(v).Int(), nil
	case uint, uint16, uint32, uint64:
		return int64(reflect.ValueOf(v).Uint()), nil
	}
	return 0, errors.New("invalid value type")
}

func coerceUint64(v interface{}) (uint64, error) {
	switch v.(type) {
	case string:
		return strconv.ParseUint(v.(string), 10, 64)
	case int, int16, int32, int64:
		return uint64(reflect.ValueOf(v).Int()), nil
	case uint, uint16, uint32, uint64:
		return reflect.ValueOf(v).Uint(), nil
	}
	return 0, errors.New("invalid value type")
}