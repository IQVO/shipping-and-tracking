Feature: Arrival and delivery facts of consumed events
  A linehaul LoadArrived whose node is a delivery station parks the package
  at that station; a last-mile DeliveryAttempted that names no node of its
  own takes the node of the journey's last applied scan (ADR 0002 A21).

  Background:
    Given customer order "ord-1" ships to a synthetic address
    And a label has been issued for package 1 of "PO-1"

  Scenario: A load arriving at a delivery station parks the package there
    Given I record a "PICKUP_DEPARTURE" scan "s1" at hour 1
    When a linehaul event arrives the package at node "sta-south" of type "DELIVERY_STATION" at hour 4
    Then the request succeeds
    And the journey is "AT_STATION"
    And the last published events are "PackageScanned"
    And I query the journey of the package
    And the last recorded scan has node "sta-south"
    And the timeline says the package was handed over at hour 1

  Scenario: A delivered attempt without a node takes the last applied node
    Given the package is out for delivery
    When a last-mile event delivers the package at hour 20 with no node of its own
    Then the request succeeds
    And the journey is "DELIVERED"
    And the last published events are "PackageScanned,PackageDelivered"
    And I query the journey of the package
    And the last recorded scan has node "node-out_for_delivery"
    And the timeline says the package was delivered at hour 20
