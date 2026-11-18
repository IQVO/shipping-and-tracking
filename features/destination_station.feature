Feature: The destination station on LabelIssued
  The delivery station of an area is kept from node-topology's
  ServiceAreaAssigned (ADR 0019) and published as the optional
  destinationStationNodeId of LabelIssued, so linehaul-transportation can plan
  a direct lane to the station. It is a node id only, never an address; the
  field is omitted when no station is known for the destination prefix.

  Background:
    Given customer order "ord-1" ships to a synthetic address

  Scenario: A label for an address in a covered area carries the area's station
    Given the area "01310" is assigned to delivery station "node-ds-07" at version 4
    When I issue a label for package 1 of "PO-1" for order "ord-1" on tier "NEXT_DAY"
    Then the request succeeds
    And the published LabelIssued carries destination station "node-ds-07"
    And no published event or response contains any part of the address

  Scenario: A label for an address with no covering station omits the field
    Given the area "99999" is assigned to delivery station "node-ds-99" at version 1
    When I issue a label for package 1 of "PO-1" for order "ord-1" on tier "NEXT_DAY"
    Then the request succeeds
    And the published LabelIssued carries no destination station
    And the wire payload of the published LabelIssued has no destinationStationNodeId

  Scenario: An area assigned without a station omits the field
    Given the area "01310" was assigned at version 4 without a delivery station
    When I issue a label for package 1 of "PO-1" for order "ord-1" on tier "NEXT_DAY"
    Then the request succeeds
    And the wire payload of the published LabelIssued has no destinationStationNodeId

  Scenario: A replayed area event fills a missing station
    Given the area "01310" was assigned at version 4 without a delivery station
    When the assignment of area "01310" is replayed at version 4 with delivery station "node-ds-07"
    And I issue a label for package 1 of "PO-1" for order "ord-1" on tier "NEXT_DAY"
    Then the request succeeds
    And the area "01310" holds delivery station "node-ds-07" at version 4
    And the published LabelIssued carries destination station "node-ds-07"

  Scenario: A replayed area event never replaces a station already known
    Given the area "01310" is assigned to delivery station "node-ds-07" at version 4
    When the assignment of area "01310" is replayed at version 4 with delivery station "node-ds-99"
    Then the area "01310" holds delivery station "node-ds-07" at version 4
