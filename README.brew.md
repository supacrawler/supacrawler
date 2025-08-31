# Complete Guide: Open-Sourcing the Supacrawler Scraper Engine

This guide provides the complete step-by-step process for extracting your scraper engine into a public `supacrawler` repository while maintaining your existing deployment pipeline.

## Overview

You will create a public repository called `supacrawler` that contains only the scraper engine code. Your private `supacrawler-app` repository will then use this as a Git submodule, keeping all your existing deployment workflows intact.

**Repository Structure After Migration:**
- **Public**: `github.com/your-org/supacrawler` (the engine)
- **Private**: `github.com/your-org/supacrawler-app` (your full platform)
- **Submodule**: `apps/scraper/` → points to the public `supacrawler` repo

---

## Part 1: Extract and Setup Public Repository

### Step 1: Create the Public Repository

1. Go to GitHub and create a new **public** repository named `supacrawler`
2. Don't initialize it with README, license, or .gitignore (we'll push the extracted code)

### Step 2: Extract Scraper Code with History

From your `~/Desktop/Startups/supacrawler-app/` directory:

```bash
# 1. Create a temporary copy of your repo for extraction
cd ~/Desktop/Startups
cp -r supacrawler-app supacrawler-extraction-temp
cd supacrawler-extraction-temp

# 2. Extract only the scraper directory and move it to the root
# This preserves git history while restructuring the files
git filter-repo --path apps/scraper/ --path-rename apps/scraper/:

# 3. Add your new public repository as remote and push
git remote add origin https://github.com/your-org/supacrawler.git
git push -u origin main

# 4. Clean up the temporary directory
cd ~/Desktop/Startups
rm -rf supacrawler-extraction-temp
```

### Step 3: Setup GoReleaser in Public Repository

Clone your new public repository and set it up:

```bash
cd ~/Desktop/Startups
git clone https://github.com/your-org/supacrawler.git
cd supacrawler

# Create GoReleaser configuration
cat > .goreleaser.yml << 'EOF'
# .goreleaser.yml
before:
  hooks:
    - go mod tidy

builds:
  - env:
      - CGO_ENABLED=0
    goos:
      - linux
      - windows
      - darwin
    goarch:
      - amd64
      - arm64
    main: ./cmd/main.go
    binary: supacrawler
    ldflags:
      - -s -w
      - -X main.version={{.Version}}
      - -X main.commit={{.Commit}}
      - -X main.date={{.Date}}

archives:
  - format: tar.gz
    name_template: >-
      {{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}{{ with .Arm }}v{{ . }}{{ end }}
    format_overrides:
      - goos: windows
        format: zip

checksum:
  name_template: 'checksums.txt'

snapshot:
  name_template: "{{ incpatch .Version }}-next"

changelog:
  sort: asc
  filters:
    exclude:
      - '^docs:'
      - '^test:'
      - 'Merge pull request'
      - 'Merge branch'

# Homebrew distribution
brews:
  - name: supacrawler
    tap:
      owner: your-org  # Replace with your GitHub org/username
      name: homebrew-tap
      token: "{{ .Env.HOMEBREW_TAP_TOKEN }}"
    commit_author:
      name: goreleaserbot
      email: bot@goreleaser.com
    homepage: "https://github.com/your-org/supacrawler"
    description: "Fast, reliable web scraper and crawler engine"
    folder: Formula
    install: |
      bin.install "supacrawler"
EOF

# Create GitHub Actions workflow for releases
mkdir -p .github/workflows
cat > .github/workflows/release.yml << 'EOF'
name: Release

on:
  push:
    tags:
      - 'v*'

jobs:
  goreleaser:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout
        uses: actions/checkout@v4
        with:
          fetch-depth: 0

      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version: '1.21'

      - name: Run GoReleaser
        uses: goreleaser/goreleaser-action@v5
        with:
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          HOMEBREW_TAP_TOKEN: ${{ secrets.HOMEBREW_TAP_TOKEN }}
EOF

# Add Apache 2.0 License
cat > LICENSE << 'EOF'
Apache License
Version 2.0, January 2004
http://www.apache.org/licenses/

Copyright 2024 Your Organization

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
EOF

# Create a clean README for the public repo
cat > README.md << 'EOF'
# Supacrawler

Fast, reliable web scraper and crawler engine built in Go.

## Installation

### Homebrew (macOS/Linux)

```bash
brew tap your-org/tap
brew install supacrawler
```

### Download Binary

Download the latest binary from the [releases page](https://github.com/your-org/supacrawler/releases).

## Usage

```bash
# Basic scraping
supacrawler scrape https://example.com

# Take screenshot
supacrawler screenshot https://example.com

# Start server mode
supacrawler server --port 8081
```

## Development

```bash
# Clone the repository
git clone https://github.com/your-org/supacrawler.git
cd supacrawler

# Run tests
go test ./...

# Build
go build -o supacrawler ./cmd/main.go
```

## License

Licensed under the Apache License 2.0. See [LICENSE](LICENSE) for details.
EOF

# Commit and push the setup files
git add .
git commit -m "feat: add GoReleaser configuration and release workflow"
git push origin main
```

---

## Part 2: Convert Private Repository to Use Submodule

### Step 4: Remove Scraper and Add Submodule

In your private repository:

```bash
cd ~/Desktop/Startups/supacrawler-app

# Remove the existing scraper directory
git rm -r apps/scraper
git commit -m "chore: remove embedded scraper (moved to public supacrawler repo)"

# Add the public repository as a submodule at the same path
git submodule add https://github.com/your-org/supacrawler.git apps/scraper
git commit -m "feat: add supacrawler engine as submodule"

# Push the changes
git push origin main
```

---

## Part 3: Release and Distribution Workflow

### Step 5: Create Your First Release

```bash
cd ~/Desktop/Startups/supacrawler

# Create and push your first release tag
git tag v0.1.0
git push origin v0.1.0
```

This will trigger the GitHub Actions workflow that:
1. Builds binaries for all platforms
2. Creates a GitHub release
3. Updates your Homebrew tap (once you set up the PAT token)

### Step 6: Setup Homebrew Tap (Optional but Recommended)

1. Create a public repository named `homebrew-tap`
2. Create a Personal Access Token with `public_repo` scope
3. Add it as `HOMEBREW_TAP_TOKEN` secret in your `supacrawler` repository
4. Update the `your-org` placeholders in `.goreleaser.yml`

---

## Part 4: Ongoing Workflow

### How Releases Work

**For Public Distribution:**
1. Make changes in the public `supacrawler` repository
2. Create and push a version tag (e.g., `v0.2.0`)
3. GoReleaser automatically builds and publishes binaries
4. Users can install with `brew install supacrawler`

**For Your Private Application:**
1. Update the submodule to point to the new version:
   ```bash
   cd ~/Desktop/Startups/supacrawler-app/apps/scraper
   git fetch --all --tags
   git checkout v0.2.0
   cd ../..
   git add apps/scraper
   git commit -m "feat(scraper): update to v0.2.0"
   git push
   ```
2. This triggers your existing deployment pipeline
3. Your application rebuilds with the new scraper version

### Optional: Automated Submodule Updates

Add this workflow to `supacrawler-app/.github/workflows/sync-submodule.yml`:

```yaml
name: Sync Scraper Submodule

on:
  schedule:
    - cron: '0 */6 * * *'  # Every 6 hours
  workflow_dispatch:

jobs:
  sync:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout
        uses: actions/checkout@v4
        with:
          token: ${{ secrets.SUBMODULE_SYNC_TOKEN }}
          submodules: true

      - name: Update submodule
        run: |
          cd apps/scraper
          git fetch --all --tags
          # Get the latest tag
          LATEST_TAG=$(git describe --tags --abbrev=0 origin/main)
          git checkout $LATEST_TAG
          cd ../..
          
          if ! git diff --quiet apps/scraper; then
            git config user.name 'github-actions[bot]'
            git config user.email 'github-actions[bot]@users.noreply.github.com'
            git add apps/scraper
            git commit -m "chore: auto-update scraper to $LATEST_TAG"
            git push
          fi
```

---

## Summary

After this migration:
- ✅ Your private deployment pipeline works unchanged
- ✅ Public users can install via `brew install supacrawler`
- ✅ You control when to update your production application
- ✅ Clean separation between public engine and private platform
- ✅ Automated binary distribution and Homebrew updates

The key insight is keeping your internal directory structure as `apps/scraper/` while making the public repository cleanly branded as `supacrawler`.