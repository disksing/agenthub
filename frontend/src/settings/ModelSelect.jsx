import { useEffect, useRef, useState } from "react";
import { ArrowClockwise } from "@phosphor-icons/react";
import { api } from "../api";
import { modelListView } from "./modelOptions";

// ModelSelect is the agent model field: a dropdown fed by the provider's
// live model list (GET /v1/providers/{id}/models). It replaces the old
// free-text input, so a saved model is always a real provider identifier.
//
// Interaction contract:
// - Switching providers aborts the in-flight request and never shows the
//   previous provider's models.
// - loading / error+retry / empty / disabled provider are explicit states.
// - A saved value missing from the loaded list is kept as an explicit
//   "saved, not currently listed" option until the user picks a new model.
export function ModelSelect({ id, providerId, providerEnabled, value, onChange }) {
  const [state, setState] = useState({ status: "loading", models: [], error: "" });
  const [retryTick, setRetryTick] = useState(0);
  // sequence invalidates stale responses when the provider changes quickly.
  const sequence = useRef(0);

  useEffect(() => {
    const ticket = ++sequence.current;
    if (!providerId) {
      setState({ status: "none", models: [], error: "" });
      return undefined;
    }
    if (!providerEnabled) {
      setState({ status: "disabled", models: [], error: "" });
      return undefined;
    }
    const controller = new AbortController();
    setState({ status: "loading", models: [], error: "" });
    api(`/v1/providers/${encodeURIComponent(providerId)}/models`, { signal: controller.signal })
      .then((body) => {
        if (controller.signal.aborted || ticket !== sequence.current) return;
        setState({ status: "ready", models: Array.isArray(body?.models) ? body.models : [], error: "" });
      })
      .catch((error) => {
        if (controller.signal.aborted || ticket !== sequence.current) return;
        setState({ status: "error", models: [], error: error?.message || "Failed to load the model list." });
      });
    return () => controller.abort();
  }, [providerId, providerEnabled, retryTick]);

  const view = modelListView(state, value);
  return (
    <div className="settings-model">
      <div className="settings-model-row">
        <select
          id={id}
          className="settings-select"
          value={value ?? ""}
          disabled={view.disabled}
          aria-busy={state.status === "loading" ? "true" : undefined}
          onChange={(event) => onChange(event.target.value)}
        >
          {view.choices.map((choice) => (
            <option key={choice.value || "__default__"} value={choice.value}>
              {choice.label}
            </option>
          ))}
        </select>
        {view.retry ? (
          <button
            type="button"
            className="icon-button"
            aria-label="Retry loading the model list"
            title="Retry"
            onClick={() => setRetryTick((tick) => tick + 1)}
          >
            <ArrowClockwise size={16} />
          </button>
        ) : null}
      </div>
      {view.message ? (
        <p
          className={`settings-model-status ${view.tone === "error" ? "settings-model-status-error" : ""}`}
          role={view.tone === "error" ? "alert" : "status"}
        >
          {view.message}
        </p>
      ) : null}
    </div>
  );
}
