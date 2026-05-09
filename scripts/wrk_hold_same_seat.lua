local responses = {}
local success = 0
local conflicts = 0
local failures = 0
local total = 0

local user_id = os.getenv("USER_ID")
local showtime_id = os.getenv("SHOWTIME_ID")
local seat_id = os.getenv("SEAT_ID")

if not user_id or user_id == "" then
  error("USER_ID is required")
end

if not showtime_id or showtime_id == "" then
  error("SHOWTIME_ID is required")
end

if not seat_id or seat_id == "" then
  error("SEAT_ID is required")
end

wrk.method = "POST"
wrk.headers["Content-Type"] = "application/json"
wrk.body = string.format(
  '{"user_id":"%s","showtime_id":"%s","seat_ids":["%s"]}',
  user_id,
  showtime_id,
  seat_id
)

request = function()
  return wrk.format(nil, "/api/v1/bookings/hold")
end

response = function(status)
  total = total + 1
  responses[status] = (responses[status] or 0) + 1

  if status == 201 then
    success = success + 1
  elseif status == 409 then
    conflicts = conflicts + 1
  else
    failures = failures + 1
  end
end

done = function()
  io.write("\nBooking hold conflict summary\n")
  io.write(string.format("Total responses: %d\n", total))
  io.write(string.format("Success responses 201: %d\n", success))
  io.write(string.format("Conflict responses 409: %d\n", conflicts))
  io.write(string.format("Other responses: %d\n", failures))

  io.write("Status breakdown:\n")
  for status, count in pairs(responses) do
    io.write(string.format("  %s: %d\n", status, count))
  end

  if success == 1 and conflicts == total - 1 and failures == 0 then
    io.write("ASSERTION PASS: exactly one request held the seat; all others conflicted.\n")
  else
    io.write("ASSERTION FAIL: expected 1 success and the rest 409 conflicts.\n")
  end
end
