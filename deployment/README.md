# Deployment

Both `dashfetcher` and `livesim2` can be compiled to single binaries
on any target platform for the Go compiler such as Windows, MacOS, and Linux.
Since the result is a single binary it is easy to start anywhere.

On Linux, `livesim2` can run as a systemd service and do structured logging
using the journald API.

An example service file is provided here as [livesim2.service][servicefile].
Make sure that the binary and vod assets are available at the paths used in the service script.

The binary can also be started and log in more console-friendly formats.
See the help text provided with `livesim2 -h` to see the options.

For Linode, there is a nice `stackscript` for a full machine setup available at
[https://cloud.linode.com/stackscripts/1189972](https://cloud.linode.com/stackscripts/1189972).

## Cross compilation

Cross-compilation can be done like (Linux on Mac example)

```sh
> GOOS=linux GOARCH=amd64 go build .
```

[servicefile]: livesim2.service
