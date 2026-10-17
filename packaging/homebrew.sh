#!/bin/zsh
# Writes Ferry's Homebrew tap (homebrew/Formula/ferry.rb and homebrew/Casks/ferry-app.rb) for the files in dist/,
# downloadable from $1 (default: the GitHub release). Publish the homebrew/ folder as the repo
# github.com/lenajeremy/homebrew-ferry, then:
#   brew install lenajeremy/ferry/ferry              Ferry for the terminal and browser, macOS and Linux
#   brew services start ferry                        …running from login
#   brew install --cask lenajeremy/ferry/ferry-app   the Mac app
set -e
cd "${0:A:h}"
version=${FERRY_VERSION:-1.0.0}
base=${1:-https://github.com/lenajeremy/ferry/releases/download/v$version}
dist=../dist
sum() { shasum -a 256 $dist/$1 | cut -d' ' -f1 }
mkdir -p homebrew/Formula homebrew/Casks

cat > homebrew/Formula/ferry.rb <<RUBY
class Ferry < Formula
  desc "Send photos, videos and files between your phone and computer over Wi-Fi"
  homepage "https://github.com/lenajeremy/ferry"
  version "$version"

  on_macos do
    on_arm do
      url "$base/ferry-cli-macos-arm64"
      sha256 "$(sum ferry-cli-macos-arm64)"
    end
    on_intel do
      url "$base/ferry-cli-macos-amd64"
      sha256 "$(sum ferry-cli-macos-amd64)"
    end
  end
  on_linux do
    on_arm do
      url "$base/ferry-cli-linux-arm64"
      sha256 "$(sum ferry-cli-linux-arm64)"
    end
    on_intel do
      url "$base/ferry-cli-linux-amd64"
      sha256 "$(sum ferry-cli-linux-amd64)"
    end
  end

  def install
    bin.install Dir["ferry-cli-*"].first => "ferry"
  end

  service do
    run [opt_bin/"ferry", "-open=false"]
    keep_alive true
    log_path var/"log/ferry.log"
    error_log_path var/"log/ferry.log"
  end

  def caveats
    <<~EOS
      Start Ferry now and whenever you log in:
        brew services start ferry
      Then open http://localhost:7070 and scan the code with your phone.
      Your files are shared from ~/Ferry.
    EOS
  end

  test do
    assert_match "the shared folder", shell_output("#{bin}/ferry -h 2>&1")
  end
end
RUBY

cat > homebrew/Casks/ferry-app.rb <<RUBY
cask "ferry-app" do
  version "$version"
  sha256 "$(sum Ferry-mac.dmg)"

  url "$base/Ferry-mac.dmg"
  name "Ferry"
  desc "Send photos, videos and files between your phone and computer over Wi-Fi"
  homepage "https://github.com/lenajeremy/ferry"

  depends_on macos: ">= :big_sur"

  app "Ferry.app"

  zap trash: [
    "~/Library/Application Support/Ferry",
    "~/Library/Preferences/app.ferry.desktop.plist",
  ]
end
RUBY
echo "wrote homebrew/ for $base"
