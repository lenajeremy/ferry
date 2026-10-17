import XCTest

/// Taps through choosing files and photos the way people do, to catch crashes there. Needs a
/// computer to pair with: `TEST_RUNNER_FERRY_QR=<the code's link>` (a demo Ferry is fine).
final class SelectingUITests: XCTestCase {
    private var app: XCUIApplication!

    override func setUpWithError() throws {
        continueAfterFailure = false
        let qr = try XCTUnwrap(ProcessInfo.processInfo.environment["FERRY_QR"], "Set TEST_RUNNER_FERRY_QR to a pairing link")
        app = XCUIApplication()
        app.launchArguments = ["-ferryQR", qr]
        app.launch()
        XCTAssertTrue(app.buttons["Select"].waitForExistence(timeout: 30), "The library never showed its files")
    }

    private func alive(_ step: String) {
        XCTAssertEqual(app.state, .runningForeground, "Ferry closed after: \(step)")
    }

    private var tiles: [XCUIElement] {
        app.buttons.matching(NSPredicate(format: "label CONTAINS ', from '")).allElementsBoundByIndex.filter(\.isHittable)
    }

    func testSelectingFiles() {
        app.buttons["Select"].tap()
        alive("tapping Select")
        for tile in tiles.prefix(6) {
            tile.tap()
            alive("selecting \(tile.label)")
        }
        for tile in tiles.prefix(3) {
            tile.tap()
            alive("deselecting \(tile.label)")
        }
        app.swipeUp()
        alive("scrolling while selecting")
        for tile in tiles.prefix(4) {
            tile.tap()
            alive("selecting after scrolling \(tile.label)")
        }
        app.buttons["Done"].tap()
        alive("tapping Done")
        app.buttons["Select"].tap()
        if app.buttons["Select all"].waitForExistence(timeout: 3) {
            app.buttons["Select all"].tap()
            alive("Select all")
        }
    }

    func testPickingPhotosAndVideos() {
        app.buttons["Photos"].tap()
        alive("opening the photo picker")
        sleep(3)
        // The picker's photos (the simulator's sample library).
        let photos = app.images.allElementsBoundByIndex.filter(\.isHittable)
        for photo in photos.prefix(4) {
            photo.tap()
            alive("picking a photo")
        }
        let add = app.buttons.matching(NSPredicate(format: "label == 'Add' OR label == 'Done'")).firstMatch
        if add.waitForExistence(timeout: 3) { add.tap() }
        alive("adding the photos")
        sleep(8)
        alive("sending the photos")
    }
}
