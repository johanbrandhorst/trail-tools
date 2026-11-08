terraform {
  required_version = ">= 1.14"

  required_providers {
    github = {
      source  = "integrations/github"
      version = "~> 6.6"
    }
    google = {
      source  = "hashicorp/google"
      version = "~> 8.6"
    }
  }

  # The same state bucket as the Google Cloud configuration in infra/, under
  # its own prefix: the repository's settings are applied separately, with
  # an admin's GitHub credentials, see the README.
  backend "gcs" {
    bucket = "trail-tools-444904-tfstate"
    prefix = "github"
  }
}

# Authenticates with the GITHUB_TOKEN environment variable, which `make
# github-plan` and `make github-apply` set from the gh CLI. Managing a
# repository's settings needs an admin, which GitHub Actions' token isn't,
# so CI doesn't apply this configuration.
provider "github" {
  owner = var.owner
}

provider "google" {
  project = var.project
}

# The project's number, for the GitHub Actions variable.
data "google_project" "this" {}
