import test from "node:test";
import assert from "node:assert/strict";
import { normalizeChatControlState } from "./chat-controls-state.ts";

test("partial control payloads normalize to a full safe state", () => {
  const ref = { conversation_id: "0", task_id: "12", exploration_id: "14", main_seg: 2 };
  const empty = normalizeChatControlState(undefined, ref);
  assert.equal(empty.session, ref);
  assert.deepEqual(empty.inputs, []);
  assert.deepEqual(empty.capabilities, {});
  assert.equal(empty.active, null);
  assert.equal(empty.paused, false);
  assert.equal(empty.revision, "0");
  assert.equal(empty.history_revision, "0");
  assert.equal(empty.context_changing, false);

  const partial = normalizeChatControlState({}, ref);
  assert.deepEqual(partial.inputs, []);
  assert.deepEqual(partial.capabilities, {});
  assert.equal(partial.session, ref);

  const inputs = [{ id: "a", kind: "turn", status: "queued" }];
  const capabilities = { retry: true };
  const full = normalizeChatControlState(
    {
      session: { ...ref, main_seg: 3 },
      revision: "7",
      history_revision: "9",
      paused: true,
      active: null,
      inputs,
      capabilities,
    },
    ref,
  );
  assert.equal(full.session.main_seg, 3);
  assert.equal(full.revision, "7");
  assert.equal(full.paused, true);
  assert.equal(full.inputs, inputs);
  assert.equal(full.capabilities, capabilities);
});
