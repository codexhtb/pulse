export type TimeRange = '15m' | '1h' | '3h' | '6h' | '12h' | '24h'
export type SearchRange = TimeRange | 'custom'

export interface OverviewData {
  range: string
  current_qps: number
  queries: number
  responses: number
  no_response: number
  no_response_pct: number
  unmatched_responses: number
  queries_today: number
  unique_clients: number
  unique_domains: number
  nxdomain: number
  nxdomain_pct: number
  servfail: number
  servfail_pct: number
  avg_latency_us: number
  p50_latency_us: number
  p95_latency_us: number
  p99_latency_us: number
  response_bytes: number
  active_sources: number
  previous: {
    current_qps: number
    queries: number
    nxdomain_pct: number
    servfail_pct: number
    p95_latency_us: number
  }
}

export interface TrafficPoint {
  time: string
  queries: number
  responses: number
  no_response: number
  unmatched_responses: number
  nxdomain: number
  servfail: number
  avg_latency_us: number
  p50_latency_us: number
  p95_latency_us: number
  latency_samples: number
  qps: number
  bucket_seconds: number
  nxdomain_pct: number
  servfail_pct: number
}

export interface DomainRow {
  domain: string
  queries: number
  nxdomain: number
  servfail: number
}

export interface ClientRow {
  client_ip: string
  queries: number
  nxdomain: number
  servfail: number
  nxdomain_pct: number
}

export interface RCodeRow {
  rcode: string
  queries: number
}

export interface SourceRow {
  source_id: string
  queries: number
  responses: number
  nxdomain: number
  nxdomain_pct: number
  servfail: number
  servfail_pct: number
  current_qps: number
  p95_latency_us: number
  last_seen: string | null
  last_message_at: string | null
  last_persisted_at: string | null
  age_seconds: number | null
  ingest_lag_seconds: number | null
  expected: boolean
  connected: boolean
  state: string
  health: 'healthy' | 'degraded' | 'offline' | string
}

export interface Anomaly {
  type: string
  severity: 'warning' | 'critical' | string
  title: string
  value: number
  threshold: number
  unit: string
  source_id?: string
  observed_at: string
}

export interface AnomaliesResponse {
  window: string
  count: number
  anomalies: Anomaly[]
}

export interface DnsEvent {
  event_time: string
  ingested_at: string
  query_time: string | null
  response_time: string | null
  outcome: 'RESPONSE' | 'NO_RESPONSE' | 'UNMATCHED_RESPONSE' | string
  tenant_id?: string
  source_id: string
  client_ip: string
  client_port: number
  protocol: string
  qname: string
  qtype: string
  qclass: string
  rcode: string
  dns_id: number
  response_bytes: number
  answer_count: number
  latency_us: number | null
  matched_query: boolean
}

export interface SearchResponse {
  range: string
  from?: string
  to?: string
  count: number
  events: DnsEvent[]
  next_cursor?: string
}

export interface ClientDetail {
  range: string
  client_ip: string
  queries: number
  responses: number
  no_response: number
  no_response_pct: number
  unmatched_responses: number
  unique_domains: number
  nxdomain: number
  nxdomain_pct: number
  servfail: number
  servfail_pct: number
  avg_latency_us: number
  p50_latency_us: number
  p95_latency_us: number
  p99_latency_us: number
  response_bytes: number
  last_seen: string
  state: string
  sources: Array<{ source_id: string; last_seen: string }>
}

export interface ControlNodeHealth {
  status: string
  mode?: string
  last_checked_at: string | null
  last_healthy_at: string | null
  last_error?: string
  last_apply_error?: string
}

export interface DNSControlNode {
  id: string
  display_name: string
  source_identity: string
  dns_service_ip: string
  control_enabled: boolean
  control_status: string
  health: ControlNodeHealth
  action?: string
  status: 'pending' | 'applied' | 'error' | 'disabled' | string
  attempts: number
  updated_at: string | null
  last_error?: string
  next_retry_at?: string | null
  agent_result?: string
}

export interface DNSBlockStatus {
  ip: string
  blocked: boolean
  desired: 'blocked' | 'unblocked'
  status: string
  created_at: string | null
  updated_at: string | null
  expires_at: string | null
  duration?: '1h' | '24h' | 'permanent'
  reason?: string
  nodes: DNSControlNode[]
}

export interface DomainDetail {
  range: string
  domain: string
  queries: number
  responses: number
  no_response: number
  no_response_pct: number
  unmatched_responses: number
  unique_clients: number
  nxdomain: number
  nxdomain_pct: number
  servfail: number
  servfail_pct: number
  avg_latency_us: number
  p50_latency_us: number
  p95_latency_us: number
  p99_latency_us: number
  response_bytes: number
  last_seen: string
  state: string
  sources: Array<{ source_id: string; last_seen: string }>
  qtypes: Array<{ qtype: string; queries: number }>
}

export interface InvestigationDomain {
  domain: string
  queries: number
  responses: number
  no_response: number
  nxdomain: number
  servfail: number
  response_bytes: number
}

export interface InvestigationClient {
  client_ip: string
  queries: number
  responses: number
  no_response: number
  nxdomain: number
  servfail: number
  response_bytes: number
}

export interface SourceDetail {
  range: string
  source_id: string
  expected: boolean
  unexpected: boolean
  state: string
  connected: boolean
  remote_address?: string
  last_message_at: string | null
  last_persisted_at: string | null
  age_seconds: number | null
  ingest_rate_per_sec: number
  frames_since_start: number
  queries_since_start: number
  responses_since_start: number
  errors_since_start: number
  queries: number
  responses: number
  no_response: number
  unmatched_responses: number
  nxdomain: number
  servfail: number
  avg_latency_us: number
}

export interface MetaResponse {
  name: string
  product: string
  api: string
  ranges: TimeRange[]
  retention: Record<string, string>
  capabilities: Record<string, boolean>
  live: {
    transport: string
    default_rate: number
    maximum_rate: number
    server_filtering: boolean
  }
  time: string
}

export interface HealthResponse {
  status: string
  service: string
  time: string
}

export interface LiveStats {
  matched_eps: number
  shown_eps: number
  suppressed: number
  queue_dropped: number
  rate_limit: number
  subscribers: number
  server_received: number
}

export interface PipelineSystem {
  status: 'healthy' | 'degraded' | 'critical' | string
  subsystems: Record<string, string>
  collector: {
    process_started_at: string
    uptime_seconds: number
    correlation_timeout_ms: number
    counters_since_start: Record<string, number>
    active_dnstap_connections: number
    pending_count: number
    pending_peak_since_start: number
    normalized_rate_per_sec: number
    last_dnstap_event_time: string | null
    pending_persistence: {
      enabled: boolean
      wal_bytes: number
      checkpoint_bytes: number
      last_checkpoint_at: string | null
      last_sync_at: string | null
      recovered_pending: number
      recovery_errors: number
      write_errors: number
      truncated_tail_bytes: number
      persistence_lag_ms: number
      unclean_recovery: boolean
      flush_interval_ms: number
      sync_interval_ms: number
      checkpoint_interval_ms: number
    }
  }
  persistence: {
    queue_depth: number
    queue_capacity: number
    queued_total: number
    inserted_total: number
    insert_rate_per_sec: number
    batch_count: number
    write_failures: number
    durable_dropped: number
    last_failure_at: string | null
    last_dropped_at: string | null
  }
  live: {
    publisher: {
      queue_depth: number
      queue_capacity: number
      sent_total: number
      sent_rate_per_sec: number
      dropped: number
      udp_errors: number
    }
    api: { sse_subscribers: number; udp_events_received: number; decode_errors: number }
  }
  api: {
    process_started_at: string
    uptime_seconds: number
    counters_since_start: Record<string, number>
    request_latency_ms: Record<string, number>
  }
  storage: {
    raw_rows: number
    raw_compressed_bytes?: number
    database_compressed_bytes?: number
    oldest_raw_event: string | null
    newest_raw_event: string | null
    active_parts?: number
    active_merges?: number
    disk_total_bytes?: number
    disk_free_bytes?: number
    disk_used_bytes?: number
    filesystem_path?: string
    filesystem_utilization_pct?: number
    filesystem_status?: string
    filesystem_warning_pct?: number
    filesystem_critical_pct?: number
    optional_metrics_unavailable?: string[]
  }
  sources: Array<{
    source_id: string
    state: string
    connected: boolean
    remote_address?: string
    last_message_at: string | null
    last_persisted_at: string | null
    age_seconds: number | null
    expected: boolean
    unexpected: boolean
    seen_since_start: boolean
    frames_received: number
    queries_received: number
    responses_received: number
    transactions_emitted: number
    no_response: number
    unmatched_responses: number
    errors: number
    transactions_per_sec: number
    control?: DNSControlNode
  }>
  control_nodes?: DNSControlNode[]
  time: string
}

export interface QueryFilters {
  range: TimeRange
  source: string
  client_ip: string
  domain: string
  qtype: string
  rcode: string
  protocol: string
  outcome: string
  failures?: boolean
  slow_us?: number
}

export interface SearchFilters extends Omit<QueryFilters, 'range'> {
  range: SearchRange
  from?: string
  to?: string
}
