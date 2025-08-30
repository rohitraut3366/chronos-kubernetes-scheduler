#!/bin/bash

# Chronos Scheduler Release Creator
# Creates a git tag to trigger the automated release workflow

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Helper functions
log_info() {
    echo -e "${BLUE}ℹ️  $1${NC}"
}

log_success() {
    echo -e "${GREEN}✅ $1${NC}"
}

log_warning() {
    echo -e "${YELLOW}⚠️  $1${NC}"
}

log_error() {
    echo -e "${RED}❌ $1${NC}"
}

# Function to show usage
usage() {
    cat << EOF
🚀 Chronos Scheduler Release Creator

Creates a git tag to trigger the automated release workflow.

USAGE:
    $0 <version> [options]

ARGUMENTS:
    version     Version to release (e.g., 1.2.3, 1.2.3-alpha1)

OPTIONS:
    -f, --force         Force create tag (overwrite if exists)
    -n, --dry-run       Show what would be done without actually doing it
    -h, --help          Show this help message

EXAMPLES:
    $0 1.2.3                    # Create stable release v1.2.3
    $0 1.3.0-beta1              # Create prerelease v1.3.0-beta1
    $0 1.2.4 --dry-run          # Preview what would happen
    $0 1.2.3 --force            # Force create even if tag exists

RELEASE PROCESS:
    1. This script creates and pushes a git tag
    2. The release workflow automatically triggers
    3. GitHub release is created with all artifacts
    
See RELEASES.md for detailed documentation.
EOF
}

# Parse command line arguments
VERSION=""
FORCE=false
DRY_RUN=false

while [[ $# -gt 0 ]]; do
    case $1 in
        -f|--force)
            FORCE=true
            shift
            ;;
        -n|--dry-run)
            DRY_RUN=true
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        -*)
            log_error "Unknown option $1"
            usage
            exit 1
            ;;
        *)
            if [[ -z "$VERSION" ]]; then
                VERSION="$1"
            else
                log_error "Too many arguments"
                usage
                exit 1
            fi
            shift
            ;;
    esac
done

# Check if version is provided
if [[ -z "$VERSION" ]]; then
    log_error "Version is required"
    usage
    exit 1
fi

# Add 'v' prefix if not present
if [[ ! "$VERSION" =~ ^v ]]; then
    VERSION="v$VERSION"
fi

# Validate version format
if [[ ! "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9]+)?$ ]]; then
    log_error "Invalid version format: $VERSION"
    echo "Expected format: v1.2.3 or v1.2.3-alpha1"
    exit 1
fi

echo "🚀 Chronos Scheduler Release Creator"
echo "=================================="
echo

log_info "Release version: $VERSION"

# Check if we're in a git repository
if ! git rev-parse --git-dir > /dev/null 2>&1; then
    log_error "Not in a git repository"
    exit 1
fi

# Check if we're on master branch
CURRENT_BRANCH=$(git symbolic-ref --short HEAD)
if [[ "$CURRENT_BRANCH" != "master" ]]; then
    log_warning "You're on branch '$CURRENT_BRANCH', not 'master'"
    read -p "Continue anyway? (y/N) " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        log_info "Aborted"
        exit 0
    fi
fi

# Check if working directory is clean
if [[ -n $(git status --porcelain) ]]; then
    log_warning "Working directory has uncommitted changes"
    git status --short
    echo
    read -p "Continue anyway? (y/N) " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        log_info "Aborted"
        exit 0
    fi
fi

# Check if tag already exists
if git rev-parse "$VERSION" >/dev/null 2>&1; then
    if [[ "$FORCE" == "true" ]]; then
        log_warning "Tag $VERSION already exists, will overwrite (--force)"
    else
        log_error "Tag $VERSION already exists"
        echo "Use --force to overwrite or choose a different version"
        exit 1
    fi
fi

# Get current commit
CURRENT_COMMIT=$(git rev-parse HEAD)
CURRENT_COMMIT_SHORT=$(git rev-parse --short HEAD)

log_info "Current commit: $CURRENT_COMMIT_SHORT"

# Check if we're behind remote
git fetch origin >/dev/null 2>&1
BEHIND_COUNT=$(git rev-list --count HEAD..origin/$CURRENT_BRANCH 2>/dev/null || echo "0")
if [[ "$BEHIND_COUNT" -gt 0 ]]; then
    log_warning "Your branch is $BEHIND_COUNT commit(s) behind origin/$CURRENT_BRANCH"
    read -p "Pull latest changes first? (Y/n) " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Nn]$ ]]; then
        log_info "Pulling latest changes..."
        git pull origin "$CURRENT_BRANCH"
        CURRENT_COMMIT=$(git rev-parse HEAD)
        CURRENT_COMMIT_SHORT=$(git rev-parse --short HEAD)
        log_success "Updated to commit: $CURRENT_COMMIT_SHORT"
    fi
fi

# Get latest existing tag for comparison
LATEST_TAG=$(git tag -l "v*.*.*" | grep -v "-" | sort -V | tail -n1 2>/dev/null || echo "")
if [[ -n "$LATEST_TAG" ]]; then
    log_info "Latest release: $LATEST_TAG"
    
    # Show commits since last tag
    COMMIT_COUNT=$(git rev-list --count $LATEST_TAG..HEAD 2>/dev/null || echo "0")
    if [[ "$COMMIT_COUNT" -gt 0 ]]; then
        log_info "$COMMIT_COUNT new commit(s) since $LATEST_TAG"
        echo
        echo "Recent commits:"
        git log --oneline --no-merges $LATEST_TAG..HEAD | head -10
    else
        log_warning "No new commits since $LATEST_TAG"
    fi
else
    log_info "No previous releases found"
fi

echo
echo "📋 Release Summary"
echo "=================="
echo "Version:    $VERSION"
echo "Commit:     $CURRENT_COMMIT_SHORT"
echo "Branch:     $CURRENT_BRANCH"

# Check version types
if [[ "$VERSION" =~ -[a-zA-Z] ]]; then
    echo "Type:       Prerelease"
else
    echo "Type:       Stable release"
fi

echo

# Dry run mode
if [[ "$DRY_RUN" == "true" ]]; then
    log_info "DRY RUN MODE - No changes will be made"
    echo
    echo "Would execute:"
    if [[ "$FORCE" == "true" ]] && git rev-parse "$VERSION" >/dev/null 2>&1; then
        echo "  git tag -d $VERSION"
        echo "  git push origin :refs/tags/$VERSION"
    fi
    echo "  git tag -a $VERSION -m 'Release $VERSION'"
    echo "  git push origin $VERSION"
    echo
    log_info "Release workflow would trigger automatically after pushing the tag"
    exit 0
fi

# Confirm before proceeding
echo "🚦 Ready to create release?"
echo "This will:"
if [[ "$FORCE" == "true" ]] && git rev-parse "$VERSION" >/dev/null 2>&1; then
    echo "  1. Delete existing tag $VERSION"
fi
echo "  1. Create git tag $VERSION"
echo "  2. Push tag to origin"
echo "  3. Trigger automated release workflow"
echo
read -p "Continue? (y/N) " -n 1 -r
echo
if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    log_info "Aborted"
    exit 0
fi

echo
log_info "Creating release..."

# Delete existing tag if force mode
if [[ "$FORCE" == "true" ]] && git rev-parse "$VERSION" >/dev/null 2>&1; then
    log_info "Deleting existing local tag..."
    git tag -d "$VERSION"
    
    log_info "Deleting existing remote tag..."
    git push origin ":refs/tags/$VERSION" || log_warning "Failed to delete remote tag (may not exist)"
fi

# Create the tag
log_info "Creating tag $VERSION..."
git tag -a "$VERSION" -m "Release $VERSION"

# Push the tag
log_info "Pushing tag to origin..."
git push origin "$VERSION"

log_success "Release tag created successfully!"
echo
echo "🎉 Release $VERSION initiated!"
echo
echo "📋 Next steps:"
echo "  1. Monitor the release workflow: https://github.com/$(git config --get remote.origin.url | sed 's/.*github.com[:/]\([^/]*\/[^.]*\).*/\1/')/actions"
echo "  2. Review the generated release: https://github.com/$(git config --get remote.origin.url | sed 's/.*github.com[:/]\([^/]*\/[^.]*\).*/\1/')/releases"
echo "  3. The release workflow will:"
echo "     - Run comprehensive tests"
echo "     - Build multi-platform binaries and container images"
echo "     - Package Helm charts"
echo "     - Generate Kubernetes manifests"
echo "     - Create GitHub release with all artifacts"
echo
log_success "Happy releasing! 🚀"
