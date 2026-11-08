# The repository existed before this configuration, so it's imported rather
# than created. Once it's in the state, these blocks do nothing.

import {
  to = github_repository.this
  id = var.repository
}

import {
  to = github_repository_dependabot_security_updates.this
  id = var.repository
}
