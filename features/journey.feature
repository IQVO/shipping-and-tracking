Feature: The package journey
  One customer-visible timeline folds the vendor, linehaul and last-mile facts.
  It only moves forward: an older scan or a scan without an edge is stored but
  not applied, a repeated scan id is ignored, and the end states are final.

  Background:
    Given customer order "ord-1" ships to a synthetic address
    And a label has been issued for package 1 of "PO-1"

  Scenario: From label to delivery
    When I record a "SHIPMENT_CONFIRMED" scan "s1" at hour 1
    And I record a "PICKUP_DEPARTURE" scan "s2" at hour 2
    And I record a "TRANSIT_ARRIVAL" scan "s3" at hour 5
    And I record a "DEPARTURE" scan "s4" at hour 6
    And I record a "STATION_ARRIVAL" scan "s5" at hour 9
    And I record a "OUT_FOR_DELIVERY" scan "s6" at hour 10
    And I record a "DELIVERED" scan "s7" at hour 12
    Then the journey is "DELIVERED"
    And the journey has 7 scans of which 7 applied
    And the published events include "PackageHandedOver" once and "PackageDelivered" once
    And the last published events are "PackageScanned,PackageDelivered"

  Scenario: Direct injection goes from handover straight to the station
    When I record a "PICKUP_DEPARTURE" scan "s1" at hour 1
    And I record a "STATION_ARRIVAL" scan "s2" at hour 3
    Then the journey is "AT_STATION"

  Scenario: A failed attempt sends the package back to the station
    Given the package is out for delivery
    When I record a "DELIVERY_FAILED" scan "f1" at hour 20
    Then the journey is "AT_STATION"

  Scenario: An older scan is stored but does not move the state
    Given I record a "PICKUP_DEPARTURE" scan "s1" at hour 5
    When I record a "DEPARTURE" scan "old" at hour 2
    Then the request is refused with "scan-out-of-order"
    And the journey is "HANDED_OVER"
    And the journey has 2 scans of which 1 applied

  Scenario: A scan without an edge from the current state is stored, not applied
    When I record a "DELIVERED" scan "x1" at hour 1
    Then the request is refused with "illegal-transition"
    And the journey is "LABELLED"
    And the journey has 1 scans of which 0 applied

  Scenario: A scan id is applied once
    Given I record a "PICKUP_DEPARTURE" scan "s1" at hour 1
    When I record a "DEPARTURE" scan "s1" at hour 2
    Then the request is refused with "duplicate-scan"
    And the journey has 1 scans of which 1 applied

  Scenario: Delivered is final
    Given the package is out for delivery
    And I record a "DELIVERED" scan "d1" at hour 20
    When I record a "DEPARTURE" scan "late" at hour 21
    Then the request is refused with "journey-closed"

  Scenario: A package that failed three attempts is returned to the sender
    Given the package is out for delivery
    And I record a "DELIVERY_FAILED" scan "f1" at hour 20
    When last-mile asks to return the package
    Then the journey is "RETURNED"
    And the last published events are "PackageReturned"

  Scenario: A return before the package is back at the station is retried later
    Given I record a "PICKUP_DEPARTURE" scan "s1" at hour 1
    When last-mile asks to return the package
    Then the request is refused with "illegal-transition"
    And the journey is "HANDED_OVER"

  Scenario: A load event names many packages and is applied once
    Given a second label has been issued for package 2 of "PO-1"
    When one linehaul event departs both packages at hour 3 from node "fc-1"
    And the same linehaul event is delivered again
    Then both journeys are "HANDED_OVER"
    And the published events include "PackageHandedOver" 2 times

  Scenario: A departure without a kind is the handover while the package is LABELLED
    When a linehaul departure without a departure kind is applied at hour 3
    Then the journey is "HANDED_OVER"
