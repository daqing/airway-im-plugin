import { useApiQuery } from "../api";
import { formatDuration, formatMetric, formatNumber, formatRelative, humanizeMetricKey } from "../format";
import type { ServiceMetrics, SystemStatus } from "../types";
import { ErrorBanner, Page, PageLoading } from "./page";

const STATUS_REFETCH_MS = 15_000;

export function Overview() {
  const status = useApiQuery<SystemStatus>(["status"], "/status", STATUS_REFETCH_MS);

  if (status.isPending) {
    return <PageLoading />;
  }
  if (status.isError) {
    return <ErrorBanner message={(status.error as Error).message} />;
  }

  const data = status.data;
  const online = data.online.available ? data.online.user_uuids.length : null;

  return (
    <Page title="Overview" subtitle={`Generated ${formatRelative(data.generated_at)} · refreshes every 15s`}>
      <div class="admin-cards">
        <StatCard label="Registered users" value={formatNumber(data.users.total)} />
        <StatCard
          label="Online now"
          value={online === null ? "—" : formatNumber(online)}
          sub={
            data.online.available
              ? "via gateway"
              : `gateway unavailable${data.online.error ? `: ${data.online.error}` : ""}`
          }
          degraded={!data.online.available}
        />
        <StatCard
          label="Outbox pending"
          value={formatNumber(data.outbox_publisher.pending)}
          sub={`oldest pending ${formatDuration(data.outbox_publisher.oldest_pending_seconds)}`}
          degraded={data.outbox_publisher.pending > 0 && data.outbox_publisher.oldest_pending_seconds > 60}
        />
        <StatCard
          label="Outbox published"
          value={formatNumber(data.outbox_publisher.published)}
          sub={`${formatNumber(data.outbox_publisher.failed_attempts)} failed attempt(s)`}
        />
      </div>

      <ServicePanel title="Gateway metrics" metrics={data.gateway} />
      <ServicePanel title="Delivery metrics" metrics={data.delivery} />
    </Page>
  );
}

function StatCard(props: { label: string; value: string; sub?: string; degraded?: boolean }) {
  return (
    <div class="admin-card">
      <div class="admin-card-label">{props.label}</div>
      <div class="admin-card-value">{props.value}</div>
      {props.sub && <div class="admin-card-sub">{props.sub}</div>}
      {props.degraded && <div class="aw-badge aw-badge-danger admin-card-badge">degraded</div>}
    </div>
  );
}

function ServicePanel(props: { title: string; metrics: ServiceMetrics }) {
  const keys = Object.keys(props.metrics.values).sort();
  return (
    <section class="admin-panel">
      <div class="admin-panel-header">
        <h2 class="admin-panel-title">{props.title}</h2>
        {props.metrics.available ? (
          <span class="aw-badge aw-badge-success">up</span>
        ) : (
          <span class="aw-badge aw-badge-danger">unreachable</span>
        )}
      </div>
      {props.metrics.available ? (
        keys.length > 0 ? (
          <dl class="admin-metrics">
            {keys.map((key) => (
              <div key={key} class="admin-metrics-row">
                <dt>{humanizeMetricKey(key)}</dt>
                <dd class="admin-mono">{formatMetric(props.metrics.values[key]!)}</dd>
              </div>
            ))}
          </dl>
        ) : (
          <p class="admin-panel-empty">No metrics exposed.</p>
        )
      ) : (
        <p class="admin-panel-empty">
          {props.metrics.error ?? "Service did not respond."} Metrics appear once the service is running.
        </p>
      )}
    </section>
  );
}
