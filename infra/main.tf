locals {
  service_name = "trail-tools"
  # The Cloud Run URL is deterministic, so it can be used before the
  # service exists.
  run_url    = "https://${local.service_name}-${data.google_project.this.number}.${var.region}.run.app"
  domain     = var.domain_enabled ? var.domain : ""
  public_url = local.domain != "" ? "https://${local.domain}" : local.run_url
  image_repo = "${var.region}-docker.pkg.dev/${var.project}/${google_artifact_registry_repository.images.repository_id}/server"
  # Keep the deployed image unless a new one is given, so plans and local
  # applies don't change it.
  image = (var.image != "" ? var.image
    : length(data.google_cloud_run_v2_service.current) > 0 ? data.google_cloud_run_v2_service.current[0].template[0].containers[0].image
  : "${local.image_repo}:latest")
  strava = var.strava_client_id != ""
}

resource "google_project_service" "services" {
  for_each = toset([
    "artifactregistry.googleapis.com",
    "billingbudgets.googleapis.com",
    "cloudresourcemanager.googleapis.com",
    "cloudscheduler.googleapis.com",
    "clouderrorreporting.googleapis.com",
    # Traces are exported with OTLP to the Telemetry API, and viewed in
    # Cloud Trace.
    "cloudtrace.googleapis.com",
    "telemetry.googleapis.com",
    "iam.googleapis.com",
    "iamcredentials.googleapis.com",
    "run.googleapis.com",
    "secretmanager.googleapis.com",
    "serviceusage.googleapis.com",
    "storage.googleapis.com",
    "sts.googleapis.com",
  ])
  service            = each.value
  disable_on_destroy = false
}

resource "google_artifact_registry_repository" "images" {
  repository_id = "trail-tools"
  location      = var.region
  format        = "DOCKER"
  description   = "Trail Tools container images"

  # Only keep the latest images, to stay within the free storage.
  cleanup_policy_dry_run = false
  cleanup_policies {
    id     = "keep-recent"
    action = "KEEP"
    most_recent_versions {
      keep_count = 3
    }
  }
  cleanup_policies {
    id     = "delete-old"
    action = "DELETE"
    condition {
      older_than = "86400s"
    }
  }

  depends_on = [google_project_service.services]
}

resource "google_service_account" "run" {
  account_id   = "trail-tools-run"
  display_name = "Trail Tools Cloud Run service"
}

# The import job parses OpenStreetMap extracts downloaded from URLs given on
# the map data page, so it gets its own account with only what imports need:
# the database, the elevation tile cache, and reporting errors and traces. A
# flaw in parsing an extract can't reach the service's other secrets, like
# Strava's client secret and the admin bootstrap token, or the trip photos.
resource "google_service_account" "import" {
  account_id   = "trail-tools-import"
  display_name = "Trail Tools OpenStreetMap import job"
}

resource "google_service_account" "scheduler" {
  account_id   = "trail-tools-scheduler"
  display_name = "Triggers Trail Tools Strava syncs"
}

# Secrets are created here, but their values are added by hand so they
# aren't stored in the Terraform state. See the README.
resource "google_secret_manager_secret" "secrets" {
  for_each  = toset(["database-url", "admin-bootstrap-token", "strava-client-secret", "strava-webhook-verify-token"])
  secret_id = each.value
  replication {
    auto {}
  }
  depends_on = [google_project_service.services]
}

resource "google_secret_manager_secret_iam_member" "run" {
  for_each  = google_secret_manager_secret.secrets
  secret_id = each.value.id
  role      = "roles/secretmanager.secretAccessor"
  member    = google_service_account.run.member
}

# The import job only needs the database.
resource "google_secret_manager_secret_iam_member" "import" {
  secret_id = google_secret_manager_secret.secrets["database-url"].id
  role      = "roles/secretmanager.secretAccessor"
  member    = google_service_account.import.member
}

resource "google_billing_budget" "budget" {
  billing_account = var.billing_account
  display_name    = "Trail Tools"

  budget_filter {
    projects               = ["projects/${data.google_project.this.number}"]
    calendar_period        = "MONTH"
    credit_types_treatment = "INCLUDE_ALL_CREDITS"
  }

  amount {
    specified_amount {
      currency_code = var.budget_currency
      units         = tostring(var.budget_amount)
    }
  }

  threshold_rules {
    threshold_percent = 0.5
  }
  threshold_rules {
    threshold_percent = 0.9
  }
  threshold_rules {
    threshold_percent = 1.0
  }
  threshold_rules {
    threshold_percent = 1.0
    spend_basis       = "FORECASTED_SPEND"
  }

  depends_on = [google_project_service.services]
}

data "google_cloud_run_v2_service" "current" {
  count    = var.service_enabled && var.image == "" ? 1 : 0
  name     = local.service_name
  location = var.region
}
