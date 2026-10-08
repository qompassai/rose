import { Rose } from "rose/browser";
import { ROSE_HOST } from "./config";

let _ollamaClient: Rose | null = null;

export const ollamaClient = new Proxy({} as Rose, {
  get(_target, prop) {
    if (!_ollamaClient) {
      _ollamaClient = new Rose({
        host: ROSE_HOST,
      });
    }
    const value = _ollamaClient[prop as keyof Rose];
    return typeof value === "function" ? value.bind(_ollamaClient) : value;
  },
});
