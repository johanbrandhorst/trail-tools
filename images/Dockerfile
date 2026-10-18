# The images the Makefile and CI run, in one place Dependabot can update:
# it updates the tags and digests in FROM lines, but not in Makefiles or
# .ko.yaml. This file isn't built. The Makefile and CI read the image of
# each stage by its name, so keep one FROM line per image, with AS and the
# name at the end.

# Runs the web app's tools: npm, tsc, oxlint, oxfmt and Tailwind.
FROM node:26.11.1-alpine@sha256:143494b1da2945f061539253adc65e4f1569ddf07da2d384c022c791a9d90a4a AS node

# Grafana with Tempo, for looking at traces locally, see make grafana.
FROM grafana/otel-lgtm:0.37.0@sha256:57b5ab1ca6619beb180174e2fccce04762470750669d043f544b4726d0582d07 AS otel-lgtm

# The base of the server's image, which ko adds the binary to. It has CA
# certificates and time zone data, and runs as a user without root.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab AS ko-base
