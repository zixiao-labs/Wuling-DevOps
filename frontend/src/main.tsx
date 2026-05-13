import { ChenRouter } from "chen-the-dawnstreak";
import { ChenRoutes } from "virtual:chen-routes";
import { createRoot } from "react-dom/client";
import { StrictMode } from "react";

import "@/styles/globals.css";
import { bindClientToAuthStore } from "@/auth/store";

bindClientToAuthStore();

const root = document.getElementById("root");
if (!root) {
  throw new Error("#root element not found in index.html");
}

createRoot(root).render(
  <StrictMode>
    <ChenRouter>
      <ChenRoutes />
    </ChenRouter>
  </StrictMode>,
);
