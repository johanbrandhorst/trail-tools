resource "google_cloud_run_v2_service" "app" {
  count    = var.service_enabled ? 1 : 0
  name     = local.service_name
  location = var.region
  ingress  = "INGRESS_TRAFFIC_ALL"

  deletion_protection = false

  template {
    service_account = google_service_account.run.email
    timeout         = "900s"
    # Scale to zero and run at most one instance, to stay within the free
    # tier and bound costs.
    scaling {
      min_instance_count = 0
      max_instance_count = 1
    }
    max_instance_request_concurrency = 80

    containers {
      image = local.image
      ports {
        container_port = 8080
      }
      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
        # Only use CPU during requests (request based billing).
        cpu_idle          = true
        startup_cpu_boost = true
      }

      dynamic "env" {
        for_each = merge(
          {
            # A soft limit on the Go runtime's memory, so that it collects
            # garbage harder rather than exceed the instance's 512 MiB, which
            # restarts the only instance. The rest is for what Go doesn't
            # count: the 55 MB binary, with the embedded frontend, and files
            # in the in-memory /tmp.
            GOMEMLIMIT             = "400MiB"
            TRAIL_TOOLS_HOSTNAME   = "0.0.0.0"
            TRAIL_TOOLS_PUBLIC_URL = local.public_url
            TRAIL_TOOLS_LOG_FORMAT = "json"
            TRAIL_TOOLS_LOG_LEVEL  = "info"
            # Errors are reported to Error Reporting rather than logged.
            TRAIL_TOOLS_ERROR_REPORTING_PROJECT = var.project
            TRAIL_TOOLS_OTLP_ENDPOINT           = "https://telemetry.googleapis.com"
            TRAIL_TOOLS_TRACE_PROJECT           = var.project
            TRAIL_TOOLS_TRACE_SAMPLE_RATE       = tostring(var.trace_sample_rate)
            TRAIL_TOOLS_IMPORT_JOB              = google_cloud_run_v2_job.import[0].id
            TRAIL_TOOLS_PHOTO_BUCKET            = google_storage_bucket.photos.name
            TRAIL_TOOLS_ACTIVITY_BUCKET         = google_storage_bucket.activity_data.name
            TRAIL_TOOLS_DEM_BUCKET              = google_storage_bucket.dem.name
          },
          local.strava ? {
            TRAIL_TOOLS_STRAVA_CLIENT_ID          = var.strava_client_id
            TRAIL_TOOLS_STRAVA_SYNC_MODE          = "scheduled"
            TRAIL_TOOLS_STRAVA_SYNC_JOB           = google_cloud_scheduler_job.strava_sync[0].id
            TRAIL_TOOLS_SCHEDULER_SERVICE_ACCOUNT = google_service_account.scheduler.email
            TRAIL_TOOLS_SCHEDULER_AUDIENCE        = "trail-tools-scheduler"
          } : {},
        )
        content {
          name  = env.key
          value = env.value
        }
      }

      dynamic "env" {
        for_each = merge(
          {
            TRAIL_TOOLS_DATABASE_URL          = "database-url"
            TRAIL_TOOLS_ADMIN_BOOTSTRAP_TOKEN = "admin-bootstrap-token"
          },
          local.strava ? {
            TRAIL_TOOLS_STRAVA_CLIENT_SECRET        = "strava-client-secret"
            TRAIL_TOOLS_STRAVA_WEBHOOK_VERIFY_TOKEN = "strava-webhook-verify-token"
          } : {},
        )
        content {
          name = env.key
          value_source {
            secret_key_ref {
              secret  = google_secret_manager_secret.secrets[env.value].secret_id
              version = "latest"
            }
          }
        }
      }
    }
  }

  depends_on = [
    google_secret_manager_secret_iam_member.run,
    google_storage_bucket_iam_member.run_photos,
    google_storage_bucket_iam_member.run_dem,
    google_storage_bucket_iam_member.run_activity_data,
    google_project_iam_member.run_error_reporting,
    google_project_iam_member.run_tracing,
  ]
}

# Imports OpenStreetMap data into the routing graph, started from the map
# data page. Imports need more memory than the service has, so the service
# runs them as executions of this job, passing the import's ID.
resource "google_cloud_run_v2_job" "import" {
  count    = var.service_enabled ? 1 : 0
  name     = "${local.service_name}-import"
  location = var.region

  deletion_protection = false

  template {
    task_count = 1
    template {
      service_account = google_service_account.import.email
      # Matches the time limit on imports, importTimeout in
      # internal/routing/imports.go, which the app relies on to tell when an
      # import stopped without recording its result.
      timeout     = "3600s"
      max_retries = 0

      containers {
        image = local.image
        args  = ["import-osm"]
        resources {
          limits = {
            cpu    = "2"
            memory = "4Gi"
          }
        }
        env {
          name  = "TRAIL_TOOLS_LOG_FORMAT"
          value = "json"
        }
        env {
          name  = "TRAIL_TOOLS_DEM_BUCKET"
          value = google_storage_bucket.dem.name
        }
        env {
          name  = "TRAIL_TOOLS_ERROR_REPORTING_PROJECT"
          value = var.project
        }
        env {
          name  = "TRAIL_TOOLS_OTLP_ENDPOINT"
          value = "https://telemetry.googleapis.com"
        }
        env {
          name  = "TRAIL_TOOLS_TRACE_PROJECT"
          value = var.project
        }
        env {
          name  = "TRAIL_TOOLS_TRACE_SAMPLE_RATE"
          value = tostring(var.trace_sample_rate)
        }
        env {
          name = "TRAIL_TOOLS_DATABASE_URL"
          value_source {
            secret_key_ref {
              secret  = google_secret_manager_secret.secrets["database-url"].secret_id
              version = "latest"
            }
          }
        }
      }
    }
  }

  depends_on = [
    google_secret_manager_secret_iam_member.import,
    google_storage_bucket_iam_member.import_dem,
    google_project_iam_member.import_error_reporting,
    google_project_iam_member.import_tracing,
  ]
}

# The service starts executions of the import job, with the import's ID as
# the arguments.
resource "google_cloud_run_v2_job_iam_member" "import" {
  count    = var.service_enabled ? 1 : 0
  name     = google_cloud_run_v2_job.import[0].name
  location = var.region
  role     = "roles/run.jobsExecutorWithOverrides"
  member   = google_service_account.run.member
}

# The app handles its own authentication.
resource "google_cloud_run_v2_service_iam_member" "public" {
  count    = var.service_enabled ? 1 : 0
  name     = google_cloud_run_v2_service.app[0].name
  location = var.region
  role     = "roles/run.invoker"
  member   = "allUsers"
}

# Cloud Run provisions and renews a TLS certificate for the domain. The
# domain must be verified, with the Terraform service account added as an
# owner, see the README.
resource "google_cloud_run_domain_mapping" "domain" {
  count    = var.service_enabled && local.domain != "" ? 1 : 0
  name     = local.domain
  location = var.region
  metadata {
    namespace = var.project
  }
  spec {
    route_name = google_cloud_run_v2_service.app[0].name
  }
}

# Syncs Strava activities hourly, as the service doesn't run between
# requests. New activities usually arrive sooner, from Strava's webhook
# events; this continues long syncs and catches anything the events missed.
# The service also runs it when a user asks to sync.
resource "google_cloud_scheduler_job" "strava_sync" {
  count            = var.service_enabled && local.strava ? 1 : 0
  name             = "strava-sync"
  region           = var.region
  schedule         = "17 * * * *"
  time_zone        = "UTC"
  attempt_deadline = "900s"

  http_target {
    uri         = "${local.public_url}/internal/strava/sync"
    http_method = "POST"
    oidc_token {
      service_account_email = google_service_account.scheduler.email
      audience              = "trail-tools-scheduler"
    }
  }

  depends_on = [google_project_service.services]
}

# Lets the service and the import job report errors to Error Reporting,
# which is free.
resource "google_project_iam_member" "run_error_reporting" {
  project = var.project
  role    = "roles/errorreporting.writer"
  member  = google_service_account.run.member

  depends_on = [google_project_service.services]
}

resource "google_project_iam_member" "import_error_reporting" {
  project = var.project
  role    = "roles/errorreporting.writer"
  member  = google_service_account.import.member

  depends_on = [google_project_service.services]
}

# Lets the service and the import job export traces, which is free up to
# 2.5 million spans a month.
resource "google_project_iam_member" "run_tracing" {
  project = var.project
  role    = "roles/cloudtrace.agent"
  member  = google_service_account.run.member

  depends_on = [google_project_service.services]
}

resource "google_project_iam_member" "import_tracing" {
  project = var.project
  role    = "roles/cloudtrace.agent"
  member  = google_service_account.import.member

  depends_on = [google_project_service.services]
}

# Lets the service run the Strava sync job when a user asks to sync.
resource "google_project_iam_member" "run_strava_sync" {
  count   = local.strava ? 1 : 0
  project = var.project
  role    = "roles/cloudscheduler.jobRunner"
  member  = google_service_account.run.member
}
