import { TransportProvider } from "@connectrpc/connect-query";
import { createConnectTransport } from "@connectrpc/connect-web";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import React from "react";
import ReactDOM from "react-dom/client";
import { createBrowserRouter } from "react-router";
import { RouterProvider } from "react-router/dom";

import App from "./app";

const rootDiv = document.querySelector("#root");
if (rootDiv === null) {
  throw new Error("Root div not found");
}
const root = ReactDOM.createRoot(rootDiv);

/**
 * Use the origin rather than a relative URL, so that RPCs work from nested
 * client side routes such as /routes/123.
 */
const transport = createConnectTransport({
  baseUrl: globalThis.location.origin,
});

const queryClient = new QueryClient();

/**
 * A data router, rather than a BrowserRouter, so that pages can block
 * navigation with useBlocker while they have unsaved changes. The app still
 * declares its routes with <Routes> in App, under this catch-all route.
 */
const router = createBrowserRouter([{ element: <App />, path: "*" }]);

root.render(
  <React.StrictMode>
    <TransportProvider transport={transport}>
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </TransportProvider>
  </React.StrictMode>
);
