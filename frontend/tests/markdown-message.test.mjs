import assert from "node:assert/strict";
import test from "node:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { MarkdownMessage } from "../src/MarkdownMessage.js";

test("agent Markdown renders common formatting and GFM", () => {
  const html = renderToStaticMarkup(createElement(MarkdownMessage, {
    text: [
      "**Done** with `inline` code.",
      "",
      "- one",
      "- two",
      "",
      "[AgentHub](https://example.com)",
      "",
      "| Name | State |",
      "| --- | --- |",
      "| build | passed |",
      "",
      "```js",
      "const ok = true;",
      "```",
    ].join("\n"),
  }));

  assert.match(html, /<strong>Done<\/strong>/);
  assert.match(html, /<code>inline<\/code>/);
  assert.match(html, /<ul>/);
  assert.match(html, /<table>/);
  assert.match(html, /href="https:\/\/example\.com"/);
  assert.match(html, /target="_blank"/);
  assert.match(html, /<pre><code class="language-js">/);
});

test("agent Markdown does not render raw or unsafe HTML", () => {
  const html = renderToStaticMarkup(createElement(MarkdownMessage, {
    text: "<script>alert('no')</script><img src=x onerror=alert(1)> [bad](javascript:alert(1))",
  }));

  assert.doesNotMatch(html, /<script|<img|onerror/i);
  assert.doesNotMatch(html, /javascript:/i);
});
