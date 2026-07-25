// 设置表单共享的小组件与错误查询 helper。

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
