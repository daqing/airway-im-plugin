import { useMemo, useState } from "react";
import type { ColumnDef } from "@tanstack/react-table";

import { useApiQuery } from "../api";
import { formatDateTime, formatNumber, formatRelative, truncate } from "../format";
import type { AdminConversation } from "../types";
import { DataTable } from "../ui/table";
import { Input } from "../ui/inputs";
import { ErrorBanner, Page, PageLoading } from "./page";

const CONVERSATIONS_REFETCH_MS = 30_000;

export function Conversations() {
  const [search, setSearch] = useState("");
  const conversations = useApiQuery<AdminConversation[]>(
    ["conversations"],
    "/conversations",
    CONVERSATIONS_REFETCH_MS,
  );

  const filtered = useMemo(() => {
    const data = conversations.data ?? [];
    const needle = search.trim().toLowerCase();
    if (!needle) return data;
    return data.filter((conversation) =>
      [conversation.title, conversation.creator_username, conversation.id]
        .filter(Boolean)
        .some((field) => (field as string).toLowerCase().includes(needle)),
    );
  }, [conversations.data, search]);

  const columns = useMemo<ColumnDef<AdminConversation, any>[]>(
    () => [
      {
        header: "Title",
        accessorKey: "title",
        cell: (ctx) => (
          <div>
            <div class="admin-cell-strong">{ctx.row.original.title ?? "Untitled group"}</div>
            <div class="admin-cell-sub admin-mono">{truncate(ctx.row.original.id, 16)}</div>
          </div>
        ),
      },
      { header: "Created by", accessorKey: "creator_username" },
      { header: "Members", accessorKey: "member_count", cell: (ctx) => formatNumber(ctx.row.original.member_count) },
      { header: "Messages", accessorKey: "message_count", cell: (ctx) => formatNumber(ctx.row.original.message_count) },
      {
        header: "Latest activity",
        accessorKey: "latest_message_at",
        cell: (ctx) => formatRelative(ctx.row.original.latest_message_at),
      },
      {
        header: "Created",
        accessorKey: "created_at",
        cell: (ctx) => formatDateTime(ctx.row.original.created_at),
      },
    ],
    [],
  );

  if (conversations.isPending) {
    return <PageLoading />;
  }
  if (conversations.isError) {
    return <ErrorBanner message={(conversations.error as Error).message} />;
  }

  return (
    <Page
      title="Conversations"
      subtitle={`${formatNumber((conversations.data ?? []).length)} group conversation(s) · click a row to inspect its messages`}
      toolbar={<Input class="admin-search" placeholder="Search title, creator, ID…" value={search} onInput={(e) => setSearch((e.target as HTMLInputElement).value)} />}
    >
      <DataTable
        columns={columns}
        data={filtered}
        pageSize={15}
        onRowClick={(conversation) => {
          window.location.hash = `#/conversations/${conversation.id}`;
        }}
        empty={{ title: "No group conversations yet", description: "Groups appear here as soon as clients create them." }}
      />
    </Page>
  );
}
