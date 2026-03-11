#!/bin/bash
# Validate Dolt Docker image version pinning
# This check prevents accidental Dolt version upgrades that could introduce
# SQL dialect incompatibilities or query plan regressions without review.

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
NC='\033[0m'

# Extract the pinned Dolt version from testdoltcommon.go
DOLT_IMAGE_LINE=$(grep 'const DoltDockerImage = ' internal/testutil/testdoltcommon.go || true)

if [ -z "$DOLT_IMAGE_LINE" ]; then
    echo -e "${RED}❌ Could not find DoltDockerImage constant in internal/testutil/testdoltcommon.go${NC}"
    exit 1
fi

# Extract the full image name (e.g., "dolthub/dolt-sql-server:1.83.0")
DOLT_IMAGE=$(echo "$DOLT_IMAGE_LINE" | sed 's/.*"\(.*\)".*/\1/')

# Extract version tag
VERSION=$(echo "$DOLT_IMAGE" | cut -d':' -f2)
REPO=$(echo "$DOLT_IMAGE" | cut -d':' -f1)

echo "Dolt Docker Image: $DOLT_IMAGE"
echo "  Repository: $REPO"
echo "  Version: $VERSION"
echo ""

# Validation checks
ERRORS=0

# Check 1: Version must not be 'latest'
if [ "$VERSION" = "latest" ]; then
    echo -e "${RED}❌ FAIL: Dolt version is set to 'latest'${NC}"
    echo "   Pinned versions are required to prevent silent test breakage."
    echo "   Update internal/testutil/testdoltcommon.go with a specific version tag."
    ERRORS=$((ERRORS + 1))
else
    echo -e "${GREEN}✓ PASS: Version is pinned (not 'latest')${NC}"
fi

# Check 2: Version must follow semver pattern (major.minor.patch)
if ! echo "$VERSION" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$'; then
    echo -e "${RED}❌ FAIL: Version does not follow semver pattern (major.minor.patch)${NC}"
    echo "   Found: $VERSION"
    echo "   Expected: e.g., 1.83.0"
    ERRORS=$((ERRORS + 1))
else
    echo -e "${GREEN}✓ PASS: Version follows semver pattern${NC}"
fi

# Check 3: Repository must be dolthub/dolt-sql-server
if [ "$REPO" != "dolthub/dolt-sql-server" ]; then
    echo -e "${RED}❌ FAIL: Repository is not dolthub/dolt-sql-server${NC}"
    echo "   Found: $REPO"
    ERRORS=$((ERRORS + 1))
else
    echo -e "${GREEN}✓ PASS: Repository is dolthub/dolt-sql-server${NC}"
fi

echo ""

# Exit with error if any check failed
if [ $ERRORS -gt 0 ]; then
    echo -e "${RED}❌ Dolt version validation failed with $ERRORS error(s)${NC}"
    exit 1
else
    echo -e "${GREEN}✓ All Dolt version checks passed${NC}"
    echo ""
    echo -e "${YELLOW}NOTE: When upgrading Dolt versions:${NC}"
    echo "  1. Review changelog: https://github.com/dolthub/dolt/releases"
    echo "  2. Test locally: go test -race -short ./..."
    echo "  3. Run integration tests: go test -race -tags=integration ./..."
    echo "  4. Document any SQL compatibility changes in commit message"
fi
