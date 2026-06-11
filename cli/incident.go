package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/fluxplane/fluxplane-plugin/management"
)

// timelineEvent is one entry in the merged incident timeline.
type timelineEvent struct {
	Time    string         `json:"time"`
	Source  string         `json:"source"`
	Kind    string         `json:"kind"`
	Summary string         `json:"summary"`
	Detail  map[string]any `json:"detail,omitempty"`
}

type incidentTimelineResult struct {
	Context   string          `json:"context,omitempty"`
	Namespace string          `json:"namespace,omitempty"`
	Since     string          `json:"since"`
	Events    []timelineEvent `json:"events"`
	Count     int             `json:"count"`
	// PreexistingAlerts counts alerts that were already firing before the
	// window — context without flooding the chronology.
	PreexistingAlerts int      `json:"preexisting_alerts,omitempty"`
	Skipped           []string `json:"skipped,omitempty"`
}

func newIncidentCommand(backend management.Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "incident",
		Short: "Incident-troubleshooting composites",
	}
	cmd.AddCommand(newIncidentTimelineCommand(backend))
	return cmd
}

func newIncidentTimelineCommand(backend management.Backend) *cobra.Command {
	var contextName string
	var namespace string
	var instance string
	var since string
	var project string
	var plain bool
	cmd := &cobra.Command{
		Use:   "timeline",
		Short: "Merge rollouts, warnings, alerts, error rates, and deployments into one chronological view",
		Long: "The \"what changed?\" command: kubernetes events (rollouts + warnings), alertmanager alerts " +
			"that started in the window, loki error-rate buckets for the namespace, and (with --project) " +
			"gitlab deployments — merged into one chronological timeline. Unconfigured sources are skipped " +
			"and listed, never fatal.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			window, err := time.ParseDuration(since)
			if err != nil {
				return fmt.Errorf("fluxplane-plugin: invalid --since %q — use Go durations like 2h or 45m", since)
			}
			result := buildIncidentTimeline(cmd.Context(), backend, incidentTimelineParams{
				Context:   strings.TrimSpace(contextName),
				Namespace: strings.TrimSpace(namespace),
				Instance:  instance,
				Window:    window,
				Project:   strings.TrimSpace(project),
				Now:       time.Now().UTC(),
			})
			if plain {
				return renderTimeline(cmd, result)
			}
			return printJSON(cmd.OutOrStdout(), result)
		},
	}
	cmd.Flags().StringVar(&contextName, "context", "", "kubeconfig context for kubernetes sources")
	cmd.Flags().StringVar(&namespace, "namespace", "", "namespace under investigation")
	cmd.Flags().StringVar(&instance, "instance", defaultInstance(), "plugin instance")
	cmd.Flags().StringVar(&since, "since", "2h", "window to reconstruct (Go duration)")
	cmd.Flags().StringVar(&project, "project", "", "gitlab project (group/app) to include deployments and pipelines from")
	cmd.Flags().BoolVar(&plain, "plain", false, "render a plain-text timeline instead of JSON")
	return cmd
}

type incidentTimelineParams struct {
	Context   string
	Namespace string
	Instance  string
	Window    time.Duration
	Project   string
	Now       time.Time
}

func buildIncidentTimeline(ctx context.Context, backend management.Backend, params incidentTimelineParams) incidentTimelineResult {
	cutoff := params.Now.Add(-params.Window)
	result := incidentTimelineResult{
		Context:   params.Context,
		Namespace: params.Namespace,
		Since:     cutoff.Format(time.RFC3339),
	}

	type sourceOutcome struct {
		events      []timelineEvent
		preexisting int
		skip        string
	}
	sources := []func() sourceOutcome{
		func() sourceOutcome {
			events, err := timelineKubernetesEvents(ctx, backend, params, cutoff)
			return sourceOutcome{events: events, skip: skipReason("kubernetes events", err)}
		},
		func() sourceOutcome {
			events, preexisting, err := timelineAlertmanagerAlerts(ctx, backend, params, cutoff)
			return sourceOutcome{events: events, preexisting: preexisting, skip: skipReason("alertmanager alerts", err)}
		},
		func() sourceOutcome {
			events, err := timelineLokiErrorBuckets(ctx, backend, params, cutoff)
			return sourceOutcome{events: events, skip: skipReason("loki error rate", err)}
		},
		func() sourceOutcome {
			if params.Project == "" {
				return sourceOutcome{}
			}
			events, err := timelineGitlabDeployments(ctx, backend, params, cutoff)
			return sourceOutcome{events: events, skip: skipReason("gitlab deployments", err)}
		},
	}
	outcomes := make([]sourceOutcome, len(sources))
	runConcurrent(len(sources), 0, func(i int) { outcomes[i] = sources[i]() })
	for _, outcome := range outcomes {
		result.Events = append(result.Events, outcome.events...)
		result.PreexistingAlerts += outcome.preexisting
		if outcome.skip != "" {
			result.Skipped = append(result.Skipped, outcome.skip)
		}
	}
	if result.Events == nil {
		result.Events = []timelineEvent{}
	}
	sort.SliceStable(result.Events, func(i, j int) bool { return result.Events[i].Time < result.Events[j].Time })
	result.Count = len(result.Events)
	sort.Strings(result.Skipped)
	return result
}

// skipReason folds "not configured" errors into a skip note; nil means the
// source contributed.
func skipReason(source string, err error) string {
	if err == nil {
		return ""
	}
	return source + ": " + err.Error()
}

func timelineKubernetesEvents(ctx context.Context, backend management.Backend, params incidentTimelineParams, cutoff time.Time) ([]timelineEvent, error) {
	if params.Context == "" && params.Namespace == "" {
		return nil, fmt.Errorf("pass --context/--namespace to include kubernetes events")
	}
	var listed struct {
		Events []struct {
			Type         string `json:"type"`
			Reason       string `json:"reason"`
			Message      string `json:"message"`
			Count        int32  `json:"count"`
			LastSeen     string `json:"last_seen"`
			InvolvedKind string `json:"involved_kind"`
			InvolvedName string `json:"involved_name"`
		} `json:"events"`
	}
	if err := invokeInto(ctx, backend, management.Ref{Name: "kubernetes"}, params.Instance, "kubernetes.event.list", map[string]any{
		"context": params.Context, "namespace": params.Namespace, "limit": 200,
	}, &listed); err != nil {
		return nil, err
	}
	// Routine Normal pod-lifecycle churn (cron jobs alone generate dozens per
	// hour) would drown the signal — keep warnings, rollouts, and unusual
	// Normal events only.
	routine := map[string]bool{
		"Scheduled": true, "Pulling": true, "Pulled": true, "Created": true,
		"Started": true, "Killing": true, "Completed": true, "SawCompletedJob": true,
	}
	var events []timelineEvent
	for _, event := range listed.Events {
		when, err := time.Parse(time.RFC3339, event.LastSeen)
		if err != nil || when.Before(cutoff) {
			continue
		}
		scaling := event.Reason == "ScalingReplicaSet" || event.Reason == "SuccessfulCreate" || event.Reason == "SuccessfulDelete"
		deploymentKind := event.InvolvedKind == "Deployment" || event.InvolvedKind == "ReplicaSet" || event.InvolvedKind == "StatefulSet" || event.InvolvedKind == "DaemonSet"
		kind := "k8s_event"
		switch {
		case event.Type == "Warning":
			kind = "k8s_warning"
		case scaling && deploymentKind:
			kind = "rollout"
		case scaling || routine[event.Reason]:
			// Cron/Job scheduling churn — routine, not a rollout.
			continue
		}
		summary := fmt.Sprintf("%s %s/%s: %s", event.Reason, event.InvolvedKind, event.InvolvedName, event.Message)
		detail := map[string]any{}
		if event.Count > 1 {
			detail["count"] = event.Count
		}
		events = append(events, timelineEvent{
			Time: when.UTC().Format(time.RFC3339), Source: "kubernetes", Kind: kind, Summary: summary, Detail: nonEmptyDetail(detail),
		})
	}
	return events, nil
}

func timelineAlertmanagerAlerts(ctx context.Context, backend management.Backend, params incidentTimelineParams, cutoff time.Time) ([]timelineEvent, int, error) {
	endpointID, err := productEndpoint(ctx, backend, "alertmanager", params.Context)
	if err != nil {
		return nil, 0, err
	}
	var listed struct {
		Alerts []struct {
			Labels      map[string]string `json:"labels"`
			Annotations map[string]string `json:"annotations"`
			StartsAt    string            `json:"starts_at"`
		} `json:"alerts"`
	}
	if err := invokeInto(ctx, backend, management.Ref{Name: "alertmanager"}, params.Instance, "alertmanager.alerts", map[string]any{
		"endpoint_ref": endpointID,
	}, &listed); err != nil {
		return nil, 0, err
	}
	var events []timelineEvent
	preexisting := 0
	for _, alert := range listed.Alerts {
		if namespace := alert.Labels["namespace"]; params.Namespace != "" && namespace != "" && namespace != params.Namespace {
			continue
		}
		when, err := time.Parse(time.RFC3339, alert.StartsAt)
		if err != nil {
			continue
		}
		if when.Before(cutoff) {
			preexisting++
			continue
		}
		summary := "alert firing: " + alert.Labels["alertname"]
		if msg := firstNonEmptyString(alert.Annotations["summary"], alert.Annotations["description"]); msg != "" {
			summary += " — " + msg
		}
		events = append(events, timelineEvent{
			Time: when.UTC().Format(time.RFC3339), Source: "alertmanager", Kind: "alert",
			Summary: summary,
			Detail:  map[string]any{"severity": alert.Labels["severity"], "labels": alert.Labels},
		})
	}
	return events, preexisting, nil
}

func timelineLokiErrorBuckets(ctx context.Context, backend management.Backend, params incidentTimelineParams, cutoff time.Time) ([]timelineEvent, error) {
	if params.Namespace == "" {
		return nil, fmt.Errorf("pass --namespace to include loki error rates")
	}
	endpointID, err := productEndpoint(ctx, backend, "loki", params.Context)
	if err != nil {
		return nil, err
	}
	bucket := params.Window / 12
	if bucket < time.Minute {
		bucket = time.Minute
	}
	query := fmt.Sprintf(`sum(count_over_time({namespace=%q} |~ "(?i)error" [%s]))`, params.Namespace, promDuration(bucket))
	var metric struct {
		Series []struct {
			Samples []struct {
				Timestamp string  `json:"timestamp"`
				Value     float64 `json:"value"`
			} `json:"samples"`
		} `json:"series"`
	}
	if err := invokeInto(ctx, backend, management.Ref{Name: "loki"}, params.Instance, "loki.metric", map[string]any{
		"endpoint_ref": endpointID, "query": query,
		"since": promDuration(params.Window), "step": promDuration(bucket),
	}, &metric); err != nil {
		return nil, err
	}
	var events []timelineEvent
	for _, series := range metric.Series {
		for _, sample := range series.Samples {
			if sample.Value <= 0 {
				continue
			}
			when, err := time.Parse(time.RFC3339, sample.Timestamp)
			if err != nil || when.Before(cutoff) {
				continue
			}
			events = append(events, timelineEvent{
				Time: when.UTC().Format(time.RFC3339), Source: "loki", Kind: "error_rate",
				Summary: fmt.Sprintf("%.0f error lines in %s window", sample.Value, promDuration(bucket)),
				Detail:  map[string]any{"count": sample.Value, "bucket": promDuration(bucket)},
			})
		}
	}
	return events, nil
}

func timelineGitlabDeployments(ctx context.Context, backend management.Backend, params incidentTimelineParams, cutoff time.Time) ([]timelineEvent, error) {
	var listed struct {
		Items []struct {
			Ref         string `json:"ref"`
			SHA         string `json:"sha"`
			Status      string `json:"status"`
			Environment string `json:"environment"`
			CreatedAt   string `json:"created_at"`
		} `json:"items"`
	}
	if err := invokeInto(ctx, backend, management.Ref{Name: "gitlab"}, params.Instance, "gitlab.deployment.list", map[string]any{
		"project": params.Project, "limit": 50,
	}, &listed); err != nil {
		return nil, err
	}
	var events []timelineEvent
	for _, deployment := range listed.Items {
		when, err := time.Parse(time.RFC3339, deployment.CreatedAt)
		if err != nil || when.Before(cutoff) {
			continue
		}
		sha := deployment.SHA
		if len(sha) > 8 {
			sha = sha[:8]
		}
		events = append(events, timelineEvent{
			Time: when.UTC().Format(time.RFC3339), Source: "gitlab", Kind: "deployment",
			Summary: fmt.Sprintf("deployed %s@%s to %s (%s)", deployment.Ref, sha, deployment.Environment, deployment.Status),
			Detail:  map[string]any{"project": params.Project, "environment": deployment.Environment, "status": deployment.Status},
		})
	}
	return events, nil
}

// productEndpoint picks the registered endpoint for a product, preferring one
// whose id carries the cluster alias when several are stored.
func productEndpoint(ctx context.Context, backend management.Backend, product, contextName string) (string, error) {
	listed, err := backend.ListEndpoints(ctx, management.EndpointListRequest{Product: product})
	if err != nil {
		return "", err
	}
	if len(listed.Endpoints) == 0 {
		return "", fmt.Errorf("no %s endpoint registered — run `fluxplane-plugin monitor connect --context <ctx>`", product)
	}
	if alias := clusterAlias(contextName); alias != "" {
		for _, record := range listed.Endpoints {
			if strings.Contains(record.ID, alias) {
				return record.ID, nil
			}
		}
	}
	return listed.Endpoints[0].ID, nil
}

func promDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d >= time.Hour && d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	if d >= time.Minute && d%time.Minute == 0 {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

func nonEmptyDetail(detail map[string]any) map[string]any {
	if len(detail) == 0 {
		return nil
	}
	return detail
}

func renderTimeline(cmd *cobra.Command, result incidentTimelineResult) error {
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, "timeline since %s", result.Since)
	if result.Namespace != "" {
		fmt.Fprintf(w, " · namespace %s", result.Namespace)
	}
	fmt.Fprintln(w)
	if result.PreexistingAlerts > 0 {
		fmt.Fprintf(w, "(%d alert(s) already firing before the window)\n", result.PreexistingAlerts)
	}
	for _, event := range result.Events {
		fmt.Fprintf(w, "%s  [%s/%s] %s\n", event.Time, event.Source, event.Kind, event.Summary)
	}
	if len(result.Events) == 0 {
		fmt.Fprintln(w, "no events in the window")
	}
	for _, skip := range result.Skipped {
		fmt.Fprintf(w, "skipped: %s\n", skip)
	}
	return nil
}
