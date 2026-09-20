import type { ComponentChildren } from "react";

import { Spinner } from "../ui/spinner";

export function Page(props: {
  title: string;
  subtitle?: ComponentChildren;
  toolbar?: ComponentChildren;
  children: ComponentChildren;
}) {
  return (
    <div>
      <header class="admin-page-header">
        <div>
          <h1 class="admin-page-title">{props.title}</h1>
          {props.subtitle && <div class="admin-page-subtitle">{props.subtitle}</div>}
        </div>
        {props.toolbar && <div class="admin-page-toolbar">{props.toolbar}</div>}
      </header>
      {props.children}
    </div>
  );
}

export function PageLoading() {
  return (
    <div class="admin-center">
      <Spinner />
    </div>
  );
}

export function ErrorBanner(props: { message: string; children?: ComponentChildren }) {
  return (
    <div class="admin-error-banner" role="alert">
      <strong>Could not load data.</strong> {props.message}
      {props.children}
    </div>
  );
}
