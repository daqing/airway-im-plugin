import { useEffect, useState } from "react";
import { render } from "react";
import type { ComponentChildren } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/preact-query";

import "./ui/airway.css";
import "./admin.css";

import { clearSession, loadSession, setUnauthorizedHandler, type AdminSession } from "./api";
import { Login } from "./pages/login";
import { Overview } from "./pages/overview";
import { Users } from "./pages/users";
import { Conversations } from "./pages/conversations";
import { ConversationMessages } from "./pages/messages";
import { ToastProvider } from "./ui/toast";
import { cx } from "./ui/cx";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { retry: 1, refetchOnWindowFocus: false },
  },
});

// Hash router: the console ships as one embedded bundle without a router
// dependency; routes are #/overview, #/users, #/conversations, #/conversations/:id.
function useHashRoute(): string {
  const current = () => {
    const hash = window.location.hash;
    if (!hash.startsWith("#") || hash === "#") return "/overview";
    return hash.slice(1);
  };
  const [route, setRoute] = useState(current);
  useEffect(() => {
    const onChange = () => setRoute(current());
    window.addEventListener("hashchange", onChange);
    return () => window.removeEventListener("hashchange", onChange);
  }, []);
  return route;
}

function navigate(path: string): void {
  window.location.hash = `#${path}`;
}

type NavItem = { path: string; label: string };

const NAV_ITEMS: NavItem[] = [
  { path: "/overview", label: "Overview" },
  { path: "/users", label: "Users" },
  { path: "/conversations", label: "Conversations" },
];

function Shell(props: { route: string; session: AdminSession; onSignOut: () => void; children: ComponentChildren }) {
  const activeBase = "/" + (props.route.split("/")[1] ?? "");

  return (
    <div class="admin-shell">
      <aside class="admin-sidebar">
        <div class="admin-brand">
          IM Admin
          <span class="admin-brand-sub">airway-im-plugin</span>
        </div>
        <nav class="admin-nav" aria-label="main">
          {NAV_ITEMS.map((item) => (
            <a
              key={item.path}
              href={`#${item.path}`}
              class={cx("admin-nav-link", activeBase === item.path && "admin-nav-link-active")}
            >
              {item.label}
            </a>
          ))}
        </nav>
        <div class="admin-sidebar-footer">
          <div class="admin-sidebar-user" title={props.session.username}>
            {props.session.username}
          </div>
          <button type="button" class="admin-signout" onClick={props.onSignOut}>
            Sign out
          </button>
        </div>
      </aside>
      <main class="admin-main">{props.children}</main>
    </div>
  );
}

function Root() {
  const route = useHashRoute();
  const [session, setSession] = useState<AdminSession | null>(() => loadSession());

  useEffect(() => {
    // Expired sessions come back as 401 from any query; drop the token so
    // the login screen takes over.
    setUnauthorizedHandler(() => {
      clearSession();
      setSession(null);
    });
    return () => setUnauthorizedHandler(null);
  }, []);

  if (!session) {
    return (
      <Login
        onSignedIn={(next) => {
          setSession(next);
          navigate("/overview");
        }}
      />
    );
  }

  const messagesMatch = route.match(/^\/conversations\/([^/]+)$/);
  let page: ComponentChildren;
  if (route.startsWith("/users")) {
    page = <Users />;
  } else if (messagesMatch) {
    page = <ConversationMessages conversationId={messagesMatch[1]!} />;
  } else if (route.startsWith("/conversations")) {
    page = <Conversations />;
  } else {
    page = <Overview />;
  }

  return (
    <Shell
      route={route}
      session={session}
      onSignOut={() => {
        clearSession();
        setSession(null);
        navigate("/overview");
      }}
    >
      {page}
    </Shell>
  );
}

const rootElement = document.getElementById("im-admin-root");
if (rootElement) {
  render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <Root />
      </ToastProvider>
    </QueryClientProvider>,
    rootElement,
  );
}
