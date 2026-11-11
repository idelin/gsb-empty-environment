require "openssl"
require "digest"

secret = "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"
date, region, service = "20150830", "us-east-1", "service"

def sign(key, msg)
  OpenSSL::HMAC.digest("SHA256", key, msg)
end

def h256(bytes)
  Digest::SHA256.hexdigest(bytes)
end

k = sign("AWS4" + secret, date)
k = sign(k, region)
k = sign(k, service)
k = sign(k, "aws4_request")

# Public AWS SigV4 get-vanilla canonical request (IAM ListUsers, 2015-08-30)
canonical = [
  "GET", "/", "Param1=value1&Param2=value2",
  "host:example.amazonaws.com\nx-amz-date:20150830T123600Z\n",
  "host;x-amz-date", h256("")
].join("\n")
sts = [
  "AWS4-HMAC-SHA256", "20150830T123600Z",
  "#{date}/#{region}/#{service}/aws4_request", h256(canonical)
].join("\n")
seed = sign(k, sts)
puts "seed    #{seed.unpack1("H*")}"

# aws-chunked chain: two data chunks plus the mandatory final empty chunk
prior = seed
["chunk-one-data!!", "chunk-two-data!!", ""].each do |data|
  sts = [
    "AWS4-HMAC-SHA256-PAYLOAD", "20150830T123600Z",
    "#{date}/#{region}/#{service}/aws4_request",
    prior.unpack1("H*"), h256(""), h256(data)
  ].join("\n")
  prior = sign(k, sts)
  puts "chunk(#{data.bytesize}) #{prior.unpack1("H*")}"
end

bad_sts = [
  "AWS4-HMAC-SHA256-PAYLOAD", "20150830T123600Z",
  "#{date}/#{region}/#{service}/aws4_request",
  seed.unpack1("H*"), h256(""), h256("tampered!!!")
].join("\n")
puts "tampered differs: #{sign(k, bad_sts) != prior}"
