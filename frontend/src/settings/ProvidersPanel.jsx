import { Cube } from "@phosphor-icons/react";
import { buildProviderSwitches } from "./providerSwitches";

function availabilityPill(item) {
  if (!item.enabled || !item.availability) return null;
  const tone = item.availabilityTone === "ok" ? "pill-ok" : item.availabilityTone === "warn" ? "pill-warn" : "pill-muted";
  return (
    <span className={`settings-pill ${tone}`} title={item.availabilityDetail || item.availability}>
      {item.availability}
    </span>
  );
}

// ProvidersPanel renders exactly four fixed enable/disable switches for the
// built-in providers. There is intentionally no provider add/delete and no
// editing of commands, arguments, environment variables or other advanced
// fields; toggling only flips the persisted enabled flag and the daemon
// preserves the underlying configuration.
export function ProvidersPanel({ config, probes, pendingId, toggleError, onToggle }) {
  const switches = buildProviderSwitches(config, probes);
  return (
    <section aria-label="Provider settings">
      <h3 className="settings-section-title">Providers</h3>
      <p className="settings-section-desc">
        Turn the built-in agent providers on or off. Disabling a provider keeps its underlying
        configuration, but its agents cannot start new sessions until it is enabled again.
        Sessions that are already running are not interrupted.
      </p>
      {toggleError ? <div className="settings-notice" role="alert">{toggleError}</div> : null}
      {switches.map((item) => {
        const pending = pendingId === item.id;
        return (
          <article className="settings-card" key={item.id}>
            <div className="settings-card-head">
              <span className="settings-card-icon"><Cube size={20} /></span>
              <div className="settings-card-title">
                <strong>{item.name}</strong>
                <span className="settings-card-meta">
                  {item.description}
                  {item.present ? "" : " Not configured yet; enabling creates the default configuration."}
                </span>
              </div>
              {availabilityPill(item)}
              <span className={`settings-pill ${item.enabled ? "pill-ok" : "pill-muted"}`}>{item.status}</span>
              <button
                type="button"
                role="switch"
                aria-checked={item.enabled}
                aria-label={item.ariaLabel}
                className={`provider-toggle ${item.enabled ? "on" : ""}`}
                disabled={Boolean(pendingId)}
                onClick={() => onToggle(item.id, !item.enabled)}
              >
                <span className="provider-toggle-thumb" aria-hidden="true" />
                <span className="provider-toggle-text">{pending ? "Saving…" : item.enabled ? "On" : "Off"}</span>
              </button>
            </div>
          </article>
        );
      })}
    </section>
  );
}
