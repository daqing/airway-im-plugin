import { useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/preact-query";
import type { ColumnDef } from "@tanstack/react-table";

import { apiPost, useApiQuery } from "../api";
import { formatDateTime, formatNumber, formatRelative, truncate } from "../format";
import type { AdminUser, RevokeResult } from "../types";
import { Button } from "../ui/button";
import { DataTable } from "../ui/table";
import { Input } from "../ui/inputs";
import { Modal } from "../ui/modal";
import { useToast } from "../ui/toast";
import { ErrorBanner, Page, PageLoading } from "./page";

const USERS_REFETCH_MS = 30_000;

export function Users() {
  const toast = useToast();
  const queryClient = useQueryClient();
  const [search, setSearch] = useState("");
  const [revokeTarget, setRevokeTarget] = useState<AdminUser | null>(null);

  const users = useApiQuery<AdminUser[]>(["users"], "/users", USERS_REFETCH_MS);

  const revoke = useMutation({
    mutationFn: (user: AdminUser) => apiPost<RevokeResult>(`/users/${encodeURIComponent(user.uuid)}/revoke`),
    onSuccess: (result) => {
      toast.success(
        `Credentials revoked for ${result.uuid} — ${result.connections_kicked} live connection(s) kicked`,
      );
      queryClient.invalidateQueries({ queryKey: ["users"] });
      queryClient.invalidateQueries({ queryKey: ["status"] });
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Revocation failed");
    },
    onSettled: () => setRevokeTarget(null),
  });

  const filtered = useMemo(() => {
    const data = users.data ?? [];
    const needle = search.trim().toLowerCase();
    if (!needle) return data;
    return data.filter((user) =>
      [user.username, user.nickname, user.email, user.uuid]
        .filter(Boolean)
        .some((field) => (field as string).toLowerCase().includes(needle)),
    );
  }, [users.data, search]);

  const columns = useMemo<ColumnDef<AdminUser, any>[]>(
    () => [
      {
        header: "User",
        accessorKey: "username",
        cell: (ctx) => (
          <div>
            <div class="admin-cell-strong">{ctx.row.original.username}</div>
            {ctx.row.original.nickname && (
              <div class="admin-cell-sub">{ctx.row.original.nickname}</div>
            )}
          </div>
        ),
      },
      {
        header: "UUID",
        accessorKey: "uuid",
        enableSorting: false,
        cell: (ctx) => (
          <button
            type="button"
            class="admin-mono admin-copy"
            title="Click to copy"
            onClick={() => {
              navigator.clipboard?.writeText(ctx.row.original.uuid).then(
                () => toast.show("UUID copied"),
                () => {},
              );
            }}
          >
            {truncate(ctx.row.original.uuid, 14)}
          </button>
        ),
      },
      {
        header: "Email",
        accessorKey: "email",
        cell: (ctx) => ctx.row.original.email ?? <span class="admin-cell-muted">—</span>,
      },
      {
        header: "Last seen",
        accessorKey: "last_seen_at",
        cell: (ctx) => formatRelative(ctx.row.original.last_seen_at),
      },
      {
        header: "Token v",
        accessorKey: "token_version",
        cell: (ctx) => <span class="admin-mono">v{ctx.row.original.token_version}</span>,
      },
      {
        header: "Created",
        accessorKey: "created_at",
        cell: (ctx) => formatDateTime(ctx.row.original.created_at),
      },
      {
        id: "actions",
        header: "",
        enableSorting: false,
        cell: (ctx) => (
          <Button
            size="sm"
            variant="danger"
            onClick={(e) => {
              e.stopPropagation();
              setRevokeTarget(ctx.row.original);
            }}
          >
            Revoke
          </Button>
        ),
      },
    ],
    [toast],
  );

  if (users.isPending) {
    return <PageLoading />;
  }
  if (users.isError) {
    return <ErrorBanner message={(users.error as Error).message} />;
  }

  return (
    <Page
      title="Users"
      subtitle={`${formatNumber((users.data ?? []).length)} registered identit(ies) · identities appear when a host-signed credential first authenticates`}
      toolbar={<Input class="admin-search" placeholder="Search username, email, UUID…" value={search} onInput={(e) => setSearch((e.target as HTMLInputElement).value)} />}
    >
      <DataTable
        columns={columns}
        data={filtered}
        pageSize={15}
        empty={{ title: "No users yet", description: "Identities show up once clients sign in with host-signed credentials." }}
      />

      <Modal
        open={revokeTarget !== null}
        onClose={() => (revoke.isPending ? undefined : setRevokeTarget(null))}
        title="Revoke credentials"
        footer={
          <>
            <Button onClick={() => setRevokeTarget(null)} disabled={revoke.isPending}>
              Cancel
            </Button>
            <Button
              variant="danger"
              loading={revoke.isPending}
              onClick={() => revokeTarget && revoke.mutate(revokeTarget)}
            >
              Revoke credentials
            </Button>
          </>
        }
      >
        {revokeTarget && (
          <p>
            Revoke every versioned credential issued to{" "}
            <strong>{revokeTarget.username}</strong> (<span class="admin-mono">{revokeTarget.uuid}</span>)?
            The user's token version is bumped, so signed credentials stop verifying, and their live
            gateway connections are kicked. Hosts keep control of unversioned credentials.
          </p>
        )}
      </Modal>
    </Page>
  );
}
