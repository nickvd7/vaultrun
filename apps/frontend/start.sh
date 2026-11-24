#!/bin/sh
# Docker sets HOSTNAME to the container name. Next.js standalone binds that
# name, which resolves to the container IP, so nothing answers on 127.0.0.1
# and the image healthcheck stays unhealthy. Set the bind address here, after
# Docker has filled the environment.
export HOSTNAME=0.0.0.0
exec node server.js
