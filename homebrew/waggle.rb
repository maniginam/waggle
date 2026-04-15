# Homebrew formula for Waggle
#
# To set up the tap:
#   1. Create a GitHub repo: maniginam/homebrew-waggle
#   2. Copy this file into that repo as Formula/waggle.rb
#   3. Update the sha256 with the actual tarball hash:
#      curl -sL https://github.com/maniginam/waggle/archive/refs/tags/v0.1.0.tar.gz | shasum -a 256
#   4. Users can then install with:
#      brew tap maniginam/waggle
#      brew install waggle

class Waggle < Formula
  desc "Model-agnostic AI agent orchestration"
  homepage "https://github.com/maniginam/waggle"
  url "https://github.com/maniginam/waggle/archive/refs/tags/v0.1.0.tar.gz"
  sha256 "REPLACE_WITH_ACTUAL_SHA256"
  license "MIT"
  version "0.1.0"

  depends_on "go" => :build

  def install
    ldflags = "-s -w -X main.version=#{version}"
    system "go", "build", *std_go_args(ldflags: ldflags), "./cmd/waggle/"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/waggle --version")
  end
end
