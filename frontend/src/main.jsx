import React from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App.jsx";
import { BeeperPage } from "./companion/BeeperPage.jsx";
import { isBeeperPath } from "./routes.js";
import "./styles.css";

const RootPage = isBeeperPath(window.location.pathname) ? BeeperPage : App;

createRoot(document.getElementById("root")).render(
  <React.StrictMode>
    <RootPage />
  </React.StrictMode>,
);
