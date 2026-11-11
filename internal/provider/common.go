// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"regexp"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/solcreek/terraform-provider-sentry/internal/sentry"
)

// slugPattern is what Sentry accepts as a team or project slug.
var slugPattern = regexp.MustCompile(`^[a-z0-9_-]+$`)

const slugPatternMessage = "must contain only lowercase letters, numbers, dashes and underscores"

// stringOrNull maps Sentry's nullable strings onto Terraform's.
func stringOrNull(s *string) types.String {
	if s == nil {
		return types.StringNull()
	}
	return types.StringValue(*s)
}

// teamIDs returns the IDs of a project's teams, sorted.
func teamIDs(p *sentry.Project) []string {
	ids := make([]string, 0, len(p.Teams))
	for _, t := range p.Teams {
		ids = append(ids, t.ID)
	}
	sort.Strings(ids)
	return ids
}

// findTeam returns the project's team with this ID or slug, or nil.
func findTeam(p *sentry.Project, idOrSlug string) *sentry.Team {
	for i := range p.Teams {
		if p.Teams[i].ID == idOrSlug {
			return &p.Teams[i]
		}
	}
	for i := range p.Teams {
		if p.Teams[i].Slug == idOrSlug {
			return &p.Teams[i]
		}
	}
	return nil
}

// keyEndpoints returns a client key's ingestion endpoints other than the two
// DSNs, which have attributes of their own (one of them sensitive).
func keyEndpoints(ctx context.Context, k *sentry.ProjectKey) (types.Map, diag.Diagnostics) {
	out := make(map[string]string, len(k.DSN))
	for name, u := range k.DSN {
		if name != "public" && name != "secret" {
			out[name] = u
		}
	}
	return types.MapValueFrom(ctx, types.StringType, out)
}
