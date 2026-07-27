import { createElement } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

const remarkPlugins = [remarkGfm];

const components = {
  a({ node: _node, ...props }) {
    return createElement("a", {
      ...props,
      target: "_blank",
      rel: "noreferrer",
    });
  },
};

export function MarkdownMessage({ text = "" }) {
  return createElement(
    "div",
    { className: "message-markdown" },
    createElement(
      ReactMarkdown,
      {
        components,
        remarkPlugins,
        skipHtml: true,
      },
      text,
    ),
  );
}
