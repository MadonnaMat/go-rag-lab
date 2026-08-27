Review the draft answer below against the conversation and any tool results. If you need to confirm a fact, you may call the read-only corpus tools (retrieve_documents, list_resources, get_resource) before deciding — do not call any tool that writes to the corpus.

Check especially: if the draft claims a document was created, appended to, or updated, verify it. There must be a real `lore_drop` tool result earlier in this conversation, and `get_resource` on that file must actually contain the claimed content. If there is no such tool result, or the file does not contain it, the draft is wrong — the change was NOT saved. Replace the answer with an honest statement that the lore was not written to the corpus and the user should ask again.

If the draft is accurate, well-supported, and coherent, respond with exactly "OK". Otherwise, respond only with a corrected final answer — no preamble, no explanation of what was wrong.

Draft answer:
%s
