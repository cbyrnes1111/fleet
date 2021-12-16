package webhooks

import (
	"context"
	"database/sql"
	"errors"
	"path"
	"strconv"
	"time"

	"github.com/fleetdm/fleet/v4/server"
	"github.com/fleetdm/fleet/v4/server/contexts/ctxerr"
	"github.com/fleetdm/fleet/v4/server/fleet"
	"github.com/fleetdm/fleet/v4/server/service"
	kitlog "github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
)

func TriggerFailingPoliciesWebhook(
	ctx context.Context,
	ds fleet.Datastore,
	logger kitlog.Logger,
	appConfig *fleet.AppConfig,
	failingPoliciesSet service.FailingPolicySet,
	now time.Time,
) error {
	if !appConfig.WebhookSettings.FailingPoliciesWebhook.Enable {
		return nil
	}

	level.Debug(logger).Log("enabled", "true")

	for _, policyID := range appConfig.WebhookSettings.FailingPoliciesWebhook.PolicyIDs {
		policy, err := ds.Policy(ctx, policyID)
		switch {
		case err == nil:
			// OK
		case errors.Is(err, sql.ErrNoRows):
			// TODO(lucas): Deal with deleted policies.
			continue
		default:
			return ctxerr.Wrapf(ctx, err, "failing to load failing policies set %d", policyID)
		}
		hosts, err := failingPoliciesSet.ListHosts(policyID)
		if err != nil {
			return ctxerr.Wrapf(ctx, err, "listing hosts for failing policies set %d", policyID)
		}
		failingHosts := make([]FailingHost, len(hosts))
		for i := range hosts {
			failingHosts[i] = makeFailingHost(hosts[i], appConfig.ServerSettings.ServerURL)
		}
		payload := FailingPoliciesPayload{
			Timestamp:    now,
			Policy:       policy,
			FailingHosts: failingHosts,
		}
		url := appConfig.WebhookSettings.FailingPoliciesWebhook.DestinationURL
		err = server.PostJSONWithTimeout(ctx, url, &payload)
		if err != nil {
			return ctxerr.Wrapf(ctx, err, "posting to '%s'", url)
		}
		if err := failingPoliciesSet.RemoveHosts(policyID, hosts); err != nil {
			return ctxerr.Wrapf(ctx, err, "removing hosts %+v from failing policies set %d", hosts, policyID)
		}
	}
	return nil
}

type FailingPoliciesPayload struct {
	Timestamp    time.Time     `json:"timestamp"`
	Policy       *fleet.Policy `json:"policy"`
	FailingHosts []FailingHost `json:"hosts"`
}

type FailingHost struct {
	ID       uint   `json:"id"`
	Hostname string `json:"hostname"`
	URL      string `json:"url"`
}

func makeFailingHost(host service.PolicySetHost, fleetServerURL string) FailingHost {
	return FailingHost{
		ID:       host.ID,
		Hostname: host.Hostname,
		URL:      path.Join(fleetServerURL, "hosts", strconv.Itoa(int(host.ID))),
	}
}
