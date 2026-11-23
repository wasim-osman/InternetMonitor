// swift-tools-version:5.9
import PackageDescription

let package = Package(
    name: "InternetMonitor",
    platforms: [.macOS(.v14)],
    targets: [
        .target(name: "IMCore", path: "Sources/IMCore"),
        .executableTarget(name: "InternetMonitor", dependencies: ["IMCore"], path: "Sources/InternetMonitor"),
        .testTarget(name: "IMCoreTests", dependencies: ["IMCore"], path: "Tests/IMCoreTests"),
    ]
)
