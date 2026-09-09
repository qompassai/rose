-- Opt-in fixture client. All payloads are synthetic; no user configuration is loaded.
vim.opt.runtimepath:prepend(assert(vim.env.ROSE_TEST_NVIM_ROOT))
local base = assert(vim.env.ROSE_TEST_BASE)
local provider = assert(vim.env.ROSE_TEST_PROVIDER)
local section = {
  base_url = base,
  model = "transport-fixture",
  timeout = 10000,
}
if base:match("^https://") then
  section.tls = {
    ca_file = assert(vim.env.ROSE_TEST_CA),
    cert_file = assert(vim.env.ROSE_TEST_CERT),
    key_file = assert(vim.env.ROSE_TEST_KEY),
  }
end
local opts = { [provider] = section }
local config = require("rose.config").resolve(opts)
local model = require("rose.native.model")
assert(model.describe(config).provider == provider, "provider selection changed")
local valid, why = model.validate(config)
assert(valid, why)
local done, request_error, message
model.chat(
  config,
  { { role = "user", content = "synthetic transport check" } },
  nil,
  function(err, response)
    request_error, message, done = err, response, true
  end
)
assert(
  vim.wait(15000, function()
    return done == true
  end, 10),
  "chat callback timed out"
)
if vim.env.ROSE_TEST_EXPECT_FAILURE == "true" then
  assert(
    type(request_error) == "string" and #request_error > 0,
    "unsafe TLS request unexpectedly succeeded"
  )
else
  assert(request_error == nil, request_error)
  assert(message and message.content == "verified fixture response", "unexpected response")
end
print("ROSE_TRANSPORT_PASS")
vim.cmd("qa!")
