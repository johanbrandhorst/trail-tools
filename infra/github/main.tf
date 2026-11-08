# The repository's settings, the same in foodplann.ing and trail-tools.
resource "github_repository" "this" {
  name        = var.repository
  description = var.description
  visibility  = var.visibility

  has_issues      = true
  has_projects    = true
  has_wiki        = false
  has_discussions = false

  # Pull requests are squashed, with their title and description as the
  # commit message. Squashing with the commits' messages instead would copy
  # every work-in-progress commit message into the default branch.
  allow_squash_merge          = true
  squash_merge_commit_title   = "PR_TITLE"
  squash_merge_commit_message = "PR_BODY"
  allow_merge_commit          = false
  allow_rebase_merge          = false
  # Pull requests can merge themselves once their checks pass, and their
  # branches are deleted once they're merged.
  allow_auto_merge       = true
  allow_update_branch    = true
  delete_branch_on_merge = true

  vulnerability_alerts = true

  # The repository is never deleted by Terraform: removing this resource
  # archives it, and prevent_destroy refuses plans that would.
  archive_on_destroy = true
  lifecycle {
    prevent_destroy = true
  }
}

# Dependabot opens pull requests for vulnerable dependencies right away,
# without the cooldown of its version updates.
resource "github_repository_dependabot_security_updates" "this" {
  repository = github_repository.this.name
  enabled    = true
}

# The default branch only takes commits whose CI checks passed, and can't be
# force-pushed or deleted. Admins can bypass it, for the rare change that
# needs to, like rewriting history.
resource "github_repository_ruleset" "default_branch" {
  count       = var.protect_default_branch ? 1 : 0
  name        = "Default branch"
  repository  = github_repository.this.name
  target      = "branch"
  enforcement = "active"

  conditions {
    ref_name {
      include = ["~DEFAULT_BRANCH"]
      exclude = []
    }
  }

  # 5 is the repository admin role.
  bypass_actors {
    actor_id    = 5
    actor_type  = "RepositoryRole"
    bypass_mode = "always"
  }

  rules {
    deletion         = true
    non_fast_forward = true

    required_status_checks {
      # Pull requests don't need to be up to date with the default branch,
      # so Dependabot's merges don't wait for a rebase.
      strict_required_status_checks_policy = false

      dynamic "required_check" {
        for_each = var.required_checks
        content {
          context = required_check.value
        }
      }
    }
  }
}

# The project CI deploys to, which the deploy job and the Terraform plan on
# pull requests read. They aren't secret.
resource "github_actions_variable" "gcp" {
  for_each = {
    GCP_PROJECT_ID     = var.project
    GCP_PROJECT_NUMBER = data.google_project.this.number
  }
  repository    = github_repository.this.name
  variable_name = each.key
  value         = each.value
}
