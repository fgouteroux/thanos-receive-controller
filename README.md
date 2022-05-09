# Thanos Receive Controller

The Thanos Receive Controller configures multiple hashrings of Thanos receivers.  
Based on an initial hashring file, the controller identifies each healthy/ready endpoint and generates a complete configuration file.

## Usage

```shell
Usage of thanos-receive-controller:
  -directory string
      Directory path to watch hashring files. (mutually exclusive with '--file')
  -endpoint-port-offset int
      Endpoint port offset to perform readiness requests. (default 1)
  -endpoint-scheme string
      Endpoint scheme to perform readiness requests. (default "http")
  -endpoint-timeout int
      Endpoint timeout to perform readiness requests. (default 5)
  -file string
      Hashring filepath to watch. (mutually exclusive with '--directory')
  -interval int
      Watcher Scheduler interval in seconds. (default 10)
  -owner string
      Set owner on generated hashring files. (default "thanos")
  -schedule
      Enable hashring files watcher scheduler.
  -verbose
      Enabled verbose mode.
  -version
      Show version.
```

Thanos Receive Controller read an hashring file and performs http request on each node to check the readiness.
If the node is down or not ready, the endpoint will be not added in the generated file.

There is a sha256 checksum check to avoid overwrite the generated file with the same content.

## Getting Started

First, provide an initial hashring with expected endpoints:

```json
[
  {
    "endpoints": [
      "thanos-receiver1.local.net:10901",
      "thanos-receiver2.local.net:10901",
      "thanos-receiver3.local.net:10901"
    ]
  }
]
```

```shell
$ thanos-rc-controller --file /etc/thanos/hashring-example1.json
INFO 2022/01/31 15:24:23 main.go:79: File /etc/thanos/hashring-example1.json saved.
```

## Scheduler mode

In schedule mode, the controller will check every N seconds the readiness of each endpoints for a given hashring config and will generate an new hashring config.
If the current config has change with the last config, the generated file will be overwritten.

```shell
$ thanos-rc-controller --directory /etc/thanos --schedule
INFO 2022/01/31 15:25:49 main.go:299: Scheduler Started (run every 10 seconds)
INFO 2022/01/31 15:25:59 main.go:279: Tick at 2022-01-31 15:25:59.167734191 +0000 GMT m=+10.001703419
INFO 2022/01/31 15:25:59 main.go:79: File /etc/thanos/hashring-example1_generated.json saved.
INFO 2022/01/31 15:25:59 main.go:79: File /etc/thanos/hashring-example2_generated.json saved.
INFO 2022/01/31 15:25:59 main.go:79: File /etc/thanos/hashring-example3_generated.json saved.
INFO 2022/01/31 15:26:09 main.go:279: Tick at 2022-01-31 15:26:09.167761543 +0000 GMT m=+20.001730753
INFO 2022/01/31 15:26:19 main.go:279: Tick at 2022-01-31 15:26:19.167749659 +0000 GMT m=+30.001718866
^C
INFO 2022/01/31 15:26:20 main.go:295: Caught SIGTERM interrupt
INFO 2022/01/31 15:26:20 main.go:301: Scheduler Stopped...
```

This is usefull to detect an endpoint failure and to avoid taht thanos Receiver forward requests to faulty node where the node configurated in hashrings file. See https://github.com/thanos-io/thanos/issues/4059.

Use it with thanos component receiver flag: (https://thanos.io/tip/components/receive.md/)
```
--receive.hashrings-file-refresh-interval=5m
                                 Refresh interval to re-read the hashring
                                 configuration file. (used as a fallback)
```
