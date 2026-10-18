# Postgres with PostGIS and pgRouting for routing on OpenStreetMap data, for
# local development and tests. Dependabot updates the image, but not to a new
# major version: production runs on Neon, whose major version this follows.
FROM postgres:18.6@sha256:74935e72241653ca55e0414067e6d8763aceb8a810eb51b452253ec3dcfc4336
# PG_MAJOR is set by the postgres image.
RUN apt-get update \
	&& apt-get install -y --no-install-recommends \
		postgresql-${PG_MAJOR}-postgis-3 \
		postgresql-${PG_MAJOR}-pgrouting \
	&& rm -rf /var/lib/apt/lists/*
# Install the extensions in the template database, so that databases created
# for tests have them without each installing them again.
COPY template-extensions.sql /docker-entrypoint-initdb.d/
