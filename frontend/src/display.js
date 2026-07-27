export function displayTime(value) {
  const date = new Date(value);
  return Number.isNaN(date.valueOf())
    ? ""
    : date.toLocaleTimeString("en-US", {
        hour: "2-digit",
        minute: "2-digit",
      });
}
