variable "project" {
  description = "The Google Cloud project ID."
  type        = string
}

variable "region" {
  description = "The region to run in."
  type        = string
}

variable "billing_account" {
  description = "The billing account ID, for the budget."
  type        = string
}

variable "budget_amount" {
  description = "The monthly budget in the billing account's currency. Alerts are sent at 50%, 90% and 100% of actual spend, and 100% of forecast spend."
  type        = number
}

variable "budget_currency" {
  description = "The billing account's currency code."
  type        = string
}

variable "github_repository_id" {
  description = "The numeric ID of the GitHub repository allowed to apply this configuration from its main branch and plan it in pull requests. Unlike its owner/name, the ID stays the same when the repository is renamed or transferred."
  type        = string
}

variable "image" {
  description = "The container image to run. CI sets this to the image it built. If empty, the currently deployed image is kept."
  type        = string
  default     = ""
}

variable "service_enabled" {
  description = "Whether to create the Cloud Run service. It requires the database-url secret to have a value."
  type        = bool
  default     = true
}

variable "domain" {
  description = "The custom domain to serve on, e.g. trail.example.com. Passkeys are bound to the domain the app is served on."
  type        = string
  default     = ""
}

variable "domain_enabled" {
  description = "Whether to map the custom domain and serve the app on it. The domain must be verified first, with the Terraform service account as an owner, see the README."
  type        = bool
  default     = false
}

variable "trace_sample_rate" {
  description = "The fraction of traces to sample, from 0 to 1. Cloud Trace is free for the first 2.5 million spans a month, which all traces stay well within at the app's traffic."
  type        = number
  default     = 1
}

variable "garmin_client_id" {
  description = "The Garmin Connect Developer Program app's client ID (consumer key). Empty to disable Garmin. The client secret is read from the garmin-client-secret secret, and the secret in the notifications' URL from garmin-webhook-secret."
  type        = string
  default     = ""
}

variable "strava_client_id" {
  description = "The Strava API application's client ID. Empty to disable Strava. The client secret is read from the strava-client-secret secret."
  type        = string
  default     = ""
}
