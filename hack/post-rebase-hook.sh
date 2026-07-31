#!/bin/bash
# Post-rebase cleanup for OpenShift downstream

# Remove upstream GitHub Actions/Dependabot (disabled in OpenShift org)
rm -rf .github
git add -A
# only commit if there are staged changes (no-op if .github/ was already absent)
git diff --cached --quiet || git commit -m "UPSTREAM: <carry>: Remove .github directory (Actions disabled in OpenShift org)"
