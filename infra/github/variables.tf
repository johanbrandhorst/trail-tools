variable "owner" {
  description = "The GitHub user or organization that owns the repository."
  type        = string
}

variable "repository" {
  description = "The repository's name, without its owner."
  type        = string
}

variable "description" {
  description = "The repository's description."
  type        = string
}

variable "visibility" {
  description = "The repository's visibility: \"public\" or \"private\"."
  type        = string
}

variable "project" {
  description = "The Google Cloud project CI deploys to, whose ID and number are set as Actions variables."
  type        = string
}

variable "required_checks" {
  description = "The CI checks that must pass before a commit reaches the default branch, by their names on GitHub, e.g. \"Run Go tests\"."
  type        = list(string)
}

variable "protect_default_branch" {
  description = "Whether to protect the default branch with a ruleset. GitHub only offers rulesets on private repositories of paid plans, so set it to false for a private repository on a free plan."
  type        = bool
  default     = true
}
