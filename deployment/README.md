# Deployment

livesim2 can run as a systemd service and do structured logging
using the journald API.

An example service file is provided here as `livesim2.service`.
Make sure that the binary and vod assets are available at the paths used in the service script.

The binary can also be started and log in more console-friendly formats.
See the help text provided with `livesim2 -h` to see the options.
