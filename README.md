# TCP Bandwidth Throttler

# Building & launching it

```
export GO111MODULE=on
go build && ./throttle
```

# Configuration

Configuration is a map of tunnels. For each tunnel map key is a listening tcp
port specification (as defined by net.Listen) and value is JSON object with
fields ```connectTo```, ```tunnelLimit``` and ```connectionLimit```. For each
inbound connection to a listening tcp port, throttle app opens outbound connection
to an address specified by ```connectTo``` and forwards traffic to it.

There are two limits associated with each tunnel - "tunnel limit" and
"connection limit". "Tunnel limit" specifies the throughput to never exceed
by all tunnel connections altogether. "Connection limit" is the throughput
to never exceed by any individual connection that belongs to a given tunnel.

Both connection and tunnel limits should be specified in ```<number><uom>```
format where ```<number>``` is non-negative and ```<uom>``` is one of
  * ```Gbps``` - billions of bits per second
  * ```Mbps``` - millions of bits per second
  * ```Kbps``` - thousands of bits per second
  * ```bps``` - bits per second
  * ```GBps``` - Gigabytes per second
  * ```MBps``` - Megabytes per second
  * ```KBps``` - Kilobytes per second
  * ```Bps``` - bytes per second

If no unit of measure is specified, bytes per second are assumed.

Beware that uppercase 'B' means bytes and lowercase 'b' means bits. Values less
than 8 bits per second are considered to be zero.

Zero value for any limit means that this particular bandwidth should not be
limited.

Be aware that throughput is limited based on both inbound and outgoing traffic
(e.g if you have 50Kbps limit for connection and you have 20Kbps inbound stream,
outbound will get limited to 30Kbps). Tunnel limits, in a similar way, take into
account both inbound and outbound stream of all connections belonging to a
tunnel.

Application loads configuration from ```config.json``` file in the current
directory (you could also use ```-config``` command-line argument to specify
another path).

To reload config, change configuration file and send SIGUSR2 to an application:
```
kill -12 $(pidof throttle)
```

# Testing

```
# 1st console:
go build && ./throttle
# 2nd console:
iperf -s -p 32166
# 3rd console:
iperf -c localhost -p 32167 -n 100M -P 3
```

It might also be useful to start [tcptrack](https://linux.die.net/man/1/tcptrack)
on a nearby console:

```
sudo tcptrack -i lo
```

# Remarks

 * I'd prefer using fsnotify instead of unobvious reloading upon SIGUSR2 signal,
   but according to assignment I cannot use anything beyond golang.org/x/*

 * Golang JSON parsers don't report an error if there are duplicate keys in JSON
   objects. Consequently if there are duplicate listening specifications in
   configuration files you won't be notified. This can be fixed if necessary.

 * Please validate bandwidth limits by using data from iperf *server* side, not
   the client one. The reason is that client side (the one that sends the data)
   takes into account time it taken OS to accept data on socket instead of time
   it actually takes to deliver that data. Linux makes pretty heavy use of
   buffering, that's why client will consider data sent much earlier than it was
   actually delivered to throttle app. This results in higher than normal
   bandwidth readings on the iperf client side.
