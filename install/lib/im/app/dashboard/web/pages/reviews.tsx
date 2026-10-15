import { useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/preact-query";
import type { ColumnDef } from "@tanstack/react-table";

import { apiPost, useApiQuery } from "../api";
import { formatDateTime, formatRelative, truncate } from "../format";
import type { AdminReview } from "../types";
import { Button } from "../ui/button";
import { DataTable } from "../ui/table";
import { Modal } from "../ui/modal";
import { Tabs } from "../ui/tabs";
import { useToast } from "../ui/toast";
import { ErrorBanner, Page } from "./page";

type Decision = { review: AdminReview; approve: boolean };

// The pre-delivery queue: a message a client or the server's own screening
// flagged is parked here instead of reaching the conversation, and an
// operator decides whether it is delivered or discarded.
export function Reviews() {
  return (
    <Page
      title="Review queue"
      subtitle="Flagged messages wait here and are not delivered until an operator approves them. Rejecting a message labels its sender, so their later messages are queued too."
    >
      <Tabs
        items={[
          { id: "pending", label: "Pending", content: <ReviewTable status="pending" /> },
          { id: "approved", label: "Approved", content: <ReviewTable status="approved" /> },
          { id: "rejected", label: "Rejected", content: <ReviewTable status="rejected" /> },
        ]}
      />
    </Page>
  );
}

function ReviewTable(props: { status: string }) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const [inspectTarget, setInspectTarget] = useState<AdminReview | null>(null);
  const [decision, setDecision] = useState<Decision | null>(null);
  const [banTarget, setBanTarget] = useState<AdminReview | null>(null);

  const reviews = useApiQuery<AdminReview[]>(["reviews", props.status], `/reviews?status=${props.status}`);
  const pending = props.status === "pending";

  const decide = useMutation({
    mutationFn: (target: Decision) =>
      apiPost<AdminReview>(
        `/reviews/${encodeURIComponent(target.review.id)}/${target.approve ? "approve" : "reject"}`,
      ),
    onSuccess: (updated, target) => {
      toast.success(
        target.approve
          ? `Message ${truncate(updated.id, 12)} approved — it is delivered to the conversation`
          : `Message ${truncate(updated.id, 12)} rejected — ${updated.sender_username} is labeled illegal`,
      );
      queryClient.invalidateQueries({ queryKey: ["reviews"] });
      queryClient.invalidateQueries({ queryKey: ["conversations"] });
      queryClient.invalidateQueries({ queryKey: ["users"] });
      queryClient.invalidateQueries({ queryKey: ["status"] });
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Review failed");
    },
    onSettled: () => setDecision(null),
  });

  const ban = useMutation({
    mutationFn: (target: AdminReview) =>
      apiPost<{ banned_user_uuid: string; connections_kicked: number }>(
        `/reviews/${encodeURIComponent(target.id)}/ban`,
      ),
    onSuccess: (result, target) => {
      toast.success(
        `${target.sender_username} is permanently banned — ${result.connections_kicked} live connection(s) dropped`,
      );
      queryClient.invalidateQueries({ queryKey: ["reviews"] });
      queryClient.invalidateQueries({ queryKey: ["users"] });
      queryClient.invalidateQueries({ queryKey: ["status"] });
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Ban failed");
    },
    onSettled: () => setBanTarget(null),
  });

  const columns = useMemo<ColumnDef<AdminReview, any>[]>(
    () => [
      {
        header: "Queued",
        accessorKey: "created_at",
        cell: (ctx) => (
          <div>
            <div>{formatRelative(ctx.row.original.created_at)}</div>
            <div class="admin-cell-sub">{formatDateTime(ctx.row.original.created_at)}</div>
          </div>
        ),
      },
      {
        header: "Sender",
        accessorKey: "sender_username",
        cell: (ctx) => (
          <div>
            <div class="admin-cell-strong">{ctx.row.original.sender_username}</div>
            {ctx.row.original.sender_nickname && (
              <div class="admin-cell-sub">{ctx.row.original.sender_nickname}</div>
            )}
          </div>
        ),
      },
      {
        header: "Conversation",
        accessorKey: "conversation_title",
        cell: (ctx) => (
          <div>
            <div>{ctx.row.original.conversation_title ?? "Direct conversation"}</div>
            <div class="admin-cell-sub">{ctx.row.original.conversation_kind}</div>
          </div>
        ),
      },
      {
        header: "Labels",
        accessorKey: "flag_labels",
        enableSorting: false,
        cell: (ctx) =>
          ctx.row.original.flag_labels.length === 0 ? (
            <span class="aw-badge aw-badge-muted">none</span>
          ) : (
            <div class="admin-label-chips">
              {ctx.row.original.flag_labels.map((label) => (
                <span key={label} class="aw-badge aw-badge-danger">
                  {label}
                </span>
              ))}
            </div>
          ),
      },
      {
        header: "Matched",
        accessorKey: "flag_matches",
        enableSorting: false,
        cell: (ctx) =>
          ctx.row.original.flag_matches.length === 0 ? (
            <span class="admin-cell-muted">—</span>
          ) : (
            <div class="admin-label-chips">
              {ctx.row.original.flag_matches.map((word) => (
                <span key={word} class="aw-badge aw-badge-muted">
                  {word}
                </span>
              ))}
            </div>
          ),
      },
      {
        header: "Content",
        accessorKey: "content",
        enableSorting: false,
        cell: (ctx) => (
          <button
            type="button"
            class="admin-content-cell"
            title="Show full message"
            onClick={() => setInspectTarget(ctx.row.original)}
          >
            {truncate(ctx.row.original.content, 70)}
          </button>
        ),
      },
      {
        header: "Decided",
        accessorKey: "reviewed_at",
        cell: (ctx) =>
          ctx.row.original.reviewed_at ? (
            <div>
              <div>{formatRelative(ctx.row.original.reviewed_at)}</div>
              <div class="admin-cell-sub">by {ctx.row.original.reviewed_by ?? "—"}</div>
            </div>
          ) : (
            <span class="admin-cell-muted">—</span>
          ),
      },
      {
        id: "actions",
        header: "",
        enableSorting: false,
        cell: (ctx) =>
          pending ? (
            <div class="admin-row-actions">
              <Button size="sm" onClick={() => setDecision({ review: ctx.row.original, approve: true })}>
                Approve
              </Button>
              <Button
                size="sm"
                variant="danger"
                onClick={() => setDecision({ review: ctx.row.original, approve: false })}
              >
                Reject
              </Button>
              <Button
                size="sm"
                variant="danger"
                title="Permanently ban the sender"
                onClick={() => setBanTarget(ctx.row.original)}
              >
                Ban
              </Button>
            </div>
          ) : null,
      },
    ],
    [pending],
  );

  if (reviews.isError) {
    return <ErrorBanner message={(reviews.error as Error).message} />;
  }

  return (
    <>
      <DataTable
        columns={columns}
        data={reviews.data ?? []}
        pageSize={15}
        loading={reviews.isPending}
        empty={{
          title: pending ? "Nothing waiting for review" : `No ${props.status} reviews`,
          description: pending
            ? "Messages flagged by a client, or by the moderation service's word list, land here."
            : undefined,
        }}
      />

      <Modal
        open={inspectTarget !== null}
        onClose={() => setInspectTarget(null)}
        title="Queued message"
        footer={
          <>
            <Button onClick={() => setInspectTarget(null)}>Close</Button>
            {pending && inspectTarget && (
              <>
                <Button
                  variant="danger"
                  onClick={() => {
                    setDecision({ review: inspectTarget, approve: false });
                    setInspectTarget(null);
                  }}
                >
                  Reject
                </Button>
                <Button
                  variant="primary"
                  onClick={() => {
                    setDecision({ review: inspectTarget, approve: true });
                    setInspectTarget(null);
                  }}
                >
                  Approve
                </Button>
              </>
            )}
          </>
        }
      >
        {inspectTarget && (
          <div class="admin-message-detail">
            <p class="admin-message-content">{inspectTarget.content}</p>
            <dl class="admin-metrics">
              <div class="admin-metrics-row">
                <dt>Review ID</dt>
                <dd class="admin-mono">{inspectTarget.id}</dd>
              </div>
              <div class="admin-metrics-row">
                <dt>Sender</dt>
                <dd>
                  {inspectTarget.sender_username}{" "}
                  <span class="admin-mono">({inspectTarget.sender_uuid})</span>
                </dd>
              </div>
              <div class="admin-metrics-row">
                <dt>Conversation</dt>
                <dd>
                  {inspectTarget.conversation_title ?? "Direct conversation"}{" "}
                  <span class="admin-mono">({inspectTarget.conversation_id})</span>
                </dd>
              </div>
              <div class="admin-metrics-row">
                <dt>Labels</dt>
                <dd>{inspectTarget.flag_labels.join(", ") || "none"}</dd>
              </div>
              <div class="admin-metrics-row">
                <dt>Matched words</dt>
                <dd>{inspectTarget.flag_matches.join(", ") || "none"}</dd>
              </div>
              <div class="admin-metrics-row">
                <dt>Queued</dt>
                <dd>{formatDateTime(inspectTarget.created_at)}</dd>
              </div>
              <div class="admin-metrics-row">
                <dt>Status</dt>
                <dd>
                  {inspectTarget.status === "pending"
                    ? "Awaiting a decision"
                    : `${inspectTarget.status} by ${inspectTarget.reviewed_by ?? "—"} ${formatDateTime(inspectTarget.reviewed_at)}`}
                </dd>
              </div>
            </dl>
          </div>
        )}
      </Modal>

      <Modal
        open={decision !== null}
        onClose={() => (decide.isPending ? undefined : setDecision(null))}
        title={decision?.approve ? "Approve message" : "Reject message"}
        footer={
          <>
            <Button onClick={() => setDecision(null)} disabled={decide.isPending}>
              Cancel
            </Button>
            <Button
              variant={decision?.approve ? "primary" : "danger"}
              loading={decide.isPending}
              onClick={() => decision && decide.mutate(decision)}
            >
              {decision?.approve ? "Approve and deliver" : "Reject and label sender"}
            </Button>
          </>
        }
      >
        {decision && (
          <p>
            {decision.approve ? (
              <>
                Deliver the message from <strong>{decision.review.sender_username}</strong> to{" "}
                {decision.review.conversation_title ?? "this direct conversation"}? Every current
                member receives it, in sequence, as if it had been sent normally.
              </>
            ) : (
              <>
                Discard the message from <strong>{decision.review.sender_username}</strong> and label
                them <span class="admin-mono">illegal</span>? They are notified that it was not
                delivered, and their later messages are queued for review.
              </>
            )}
          </p>
        )}
      </Modal>

      <Modal
        open={banTarget !== null}
        onClose={() => (ban.isPending ? undefined : setBanTarget(null))}
        title="Ban user permanently"
        footer={
          <>
            <Button onClick={() => setBanTarget(null)} disabled={ban.isPending}>
              Cancel
            </Button>
            <Button
              variant="danger"
              loading={ban.isPending}
              onClick={() => banTarget && ban.mutate(banTarget)}
            >
              Ban permanently
            </Button>
          </>
        }
      >
        {banTarget && (
          <p>
            Permanently ban <strong>{banTarget.sender_username}</strong>? The message is discarded,
            their account can no longer log in, and any live connection drops immediately. This
            cannot be undone from the console.
          </p>
        )}
      </Modal>
    </>
  );
}
