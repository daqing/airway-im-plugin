import { useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/preact-query";
import type { ColumnDef } from "@tanstack/react-table";

import { apiPost, useApiQuery } from "../api";
import { formatDateTime, formatNumber, formatRelative, truncate } from "../format";
import type { AdminConversation, AdminMessage } from "../types";
import { Button } from "../ui/button";
import { DataTable } from "../ui/table";
import { Modal } from "../ui/modal";
import { useToast } from "../ui/toast";
import { ErrorBanner, Page, PageLoading } from "./page";

export function ConversationMessages(props: { conversationId: string }) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const [inspectTarget, setInspectTarget] = useState<AdminMessage | null>(null);
  const [moderateTarget, setModerateTarget] = useState<AdminMessage | null>(null);

  const conversations = useApiQuery<AdminConversation[]>(["conversations"], "/conversations");
  const messages = useApiQuery<AdminMessage[]>(
    ["conversations", props.conversationId, "messages"],
    `/conversations/${encodeURIComponent(props.conversationId)}/messages`,
  );

  const markIllegal = useMutation({
    mutationFn: (message: AdminMessage) =>
      apiPost<AdminMessage>(`/messages/${encodeURIComponent(message.id)}/mark-illegal`),
    onSuccess: (updated) => {
      toast.success(`Message ${truncate(updated.id, 12)} marked illegal — members are being notified`);
      queryClient.invalidateQueries({ queryKey: ["conversations"] });
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Moderation failed");
    },
    onSettled: () => setModerateTarget(null),
  });

  const conversation = (conversations.data ?? []).find((c) => c.id === props.conversationId);
  const columns = useMemo<ColumnDef<AdminMessage, any>[]>(
    () => [
      { header: "Seq", accessorKey: "sequence", cell: (ctx) => <span class="admin-mono">#{ctx.row.original.sequence}</span> },
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
            {truncate(ctx.row.original.content, 90)}
          </button>
        ),
      },
      { header: "Type", accessorKey: "content_type" },
      {
        header: "Sent",
        accessorKey: "created_at",
        cell: (ctx) => formatDateTime(ctx.row.original.created_at),
      },
      {
        header: "Status",
        accessorKey: "is_illegal",
        cell: (ctx) =>
          ctx.row.original.is_illegal ? (
            <span class="aw-badge aw-badge-danger" title={formatDateTime(ctx.row.original.moderated_at)}>
              illegal
            </span>
          ) : (
            <span class="aw-badge aw-badge-muted">ok</span>
          ),
      },
      {
        id: "actions",
        header: "",
        enableSorting: false,
        cell: (ctx) =>
          ctx.row.original.is_illegal ? null : (
            <Button
              size="sm"
              variant="danger"
              onClick={(e) => {
                e.stopPropagation();
                setModerateTarget(ctx.row.original);
              }}
            >
              Mark illegal
            </Button>
          ),
      },
    ],
    [],
  );

  if (messages.isPending || conversations.isPending) {
    return <PageLoading />;
  }
  if (messages.isError) {
    return <ErrorBanner message={(messages.error as Error).message} />;
  }
  if (!conversation) {
    return (
      <ErrorBanner message={`Group conversation ${props.conversationId} was not found.`}>
        <p>
          <a href="#/conversations">← Back to conversations</a>
        </p>
      </ErrorBanner>
    );
  }

  return (
    <Page
      title={conversation.title ?? "Untitled group"}
      subtitle={
        <>
          <a href="#/conversations">← Conversations</a>
          <span>
            {" · "}{formatNumber(conversation.member_count)} member(s) ·{" "}
            {formatNumber(conversation.message_count)} message(s) · created by{" "}
            {conversation.creator_username} {formatRelative(conversation.created_at)}
          </span>
        </>
      }
    >
      <DataTable
        columns={columns}
        data={messages.data ?? []}
        pageSize={20}
        empty={{ title: "No messages in this conversation yet" }}
      />

      <Modal
        open={inspectTarget !== null}
        onClose={() => setInspectTarget(null)}
        title={`Message #${inspectTarget?.sequence ?? ""}`}
        footer={
          <>
            <Button onClick={() => setInspectTarget(null)}>Close</Button>
            {inspectTarget && !inspectTarget.is_illegal && (
              <Button
                variant="danger"
                onClick={() => {
                  setModerateTarget(inspectTarget);
                  setInspectTarget(null);
                }}
              >
                Mark illegal
              </Button>
            )}
          </>
        }
      >
        {inspectTarget && (
          <div class="admin-message-detail">
            <p class="admin-message-content">{inspectTarget.content}</p>
            <dl class="admin-metrics">
              <div class="admin-metrics-row">
                <dt>Message ID</dt>
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
                <dt>Sent</dt>
                <dd>{formatDateTime(inspectTarget.created_at)}</dd>
              </div>
              <div class="admin-metrics-row">
                <dt>Status</dt>
                <dd>
                  {inspectTarget.is_illegal
                    ? `Marked illegal ${formatDateTime(inspectTarget.moderated_at)}`
                    : "No moderation action"}
                </dd>
              </div>
            </dl>
          </div>
        )}
      </Modal>

      <Modal
        open={moderateTarget !== null}
        onClose={() => (markIllegal.isPending ? undefined : setModerateTarget(null))}
        title="Mark message illegal"
        footer={
          <>
            <Button onClick={() => setModerateTarget(null)} disabled={markIllegal.isPending}>
              Cancel
            </Button>
            <Button
              variant="danger"
              loading={markIllegal.isPending}
              onClick={() => moderateTarget && markIllegal.mutate(moderateTarget)}
            >
              Mark illegal
            </Button>
          </>
        }
      >
        {moderateTarget && (
          <p>
            Flag message <span class="admin-mono">#{moderateTarget.sequence}</span> from{" "}
            <strong>{moderateTarget.sender_username}</strong> as illegal? Every current member of the
            conversation is notified through the gateway, and the message stays flagged for audits.
          </p>
        )}
      </Modal>
    </Page>
  );
}
