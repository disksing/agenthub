// Small shared form components and an error lookup helper for the settings UI.

export function fieldError(errors, section, index, field) {
  return errors.find((item) => item.section === section && item.index === index && item.field === field)?.message || "";
}

export function Field({ label, htmlFor, error, children }) {
  return (
    <div className={`settings-field ${error ? "settings-field-invalid" : ""}`}>
      <label htmlFor={htmlFor}>{label}</label>
      {children}
      {error ? <p className="settings-field-error" role="alert">{error}</p> : null}
    </div>
  );
}

export function FieldError({ error }) {
  return error ? <p className="settings-field-error" role="alert">{error.message || error}</p> : null;
}

export function Toggle({ checked, label, onChange, disabled = false }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      className={`provider-toggle ${checked ? "on" : ""}`}
      disabled={disabled}
      onClick={() => onChange(!checked)}
    >
      <span className="provider-toggle-thumb" aria-hidden="true" />
      <span className="provider-toggle-text">{checked ? "On" : "Off"}</span>
    </button>
  );
}
