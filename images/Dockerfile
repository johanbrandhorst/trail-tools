# The images and tools the Makefile and CI use, in one place Dependabot can
# update: it updates the tags and digests in FROM lines, but not in Makefiles,
# workflows or .ko.yaml. This file isn't built. images/image.sh reads the
# image of each stage by its name, so keep one FROM line per image, with AS
# and the name at the end.

# Runs the web app's tools: npm, tsc, oxlint, oxfmt and Tailwind.
FROM node:26.11.1-alpine@sha256:143494b1da2945f061539253adc65e4f1569ddf07da2d384c022c791a9d90a4a AS node

# The base of the server's image, which ko adds the binary to. It has CA
# certificates and time zone data, and runs as a user without root.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab AS ko-base

# Only its tag is used: the version of Terraform that CI, and the Makefile
# locally, install to plan and apply infra/.
FROM hashicorp/terraform:1.14.8@sha256:42ecfb253183ec823646dd7859c5652039669409b44daa72abf57112e622849a AS terraform
